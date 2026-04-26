import json
import os
import sys
from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class WrapperRequest:
    modelPath: str
    backendPreference: list[str]
    allowCPUFallback: bool
    messages: list[dict[str, Any]]
    enableConstrainedDecoding: bool
    constraintProvider: str
    decodingConstraint: dict[str, Any]


@dataclass(frozen=True)
class EngineSelection:
    engine: Any
    selectedBackend: str


def main() -> int:
    suppressNativeLogs()
    try:
        request = parseRequest(sys.stdin.read())
        response = generateResponse(request)
        writeResponse({"content": response["content"], "selectedBackend": response["selectedBackend"]})
        return 0
    except Exception as exception:
        writeResponse({"error": str(exception)})
        return 1


def parseRequest(document: str) -> WrapperRequest:
    payload = json.loads(document)
    return WrapperRequest(
        modelPath=requireString(payload, "modelPath"),
        backendPreference=normalizeBackendPreference(payload.get("backendPreference")),
        allowCPUFallback=bool(payload.get("allowCPUFallback", False)),
        messages=normalizeMessages(payload.get("messages", [])),
        enableConstrainedDecoding=bool(payload.get("enableConstrainedDecoding", False)),
        constraintProvider=str(payload.get("constraintProvider", "llguidance")),
        decodingConstraint=dict(payload.get("decodingConstraint", {})),
    )


def generateResponse(request: WrapperRequest) -> dict[str, str]:
    litertLM = importLiteRTLM()
    selection = createEngineSelection(litertLM, request)
    with selection.engine as engine:
        prefaceMessages, userPrompt = splitConversationMessages(request)
        with engine.create_conversation(messages=prefaceMessages) as conversation:
            modelResponse = conversation.send_message(userPrompt)
    return {
        "content": extractText(modelResponse),
        "selectedBackend": selection.selectedBackend,
    }


def importLiteRTLM() -> Any:
    import litert_lm

    litert_lm.set_min_log_severity(litert_lm.LogSeverity.ERROR)
    return litert_lm


def suppressNativeLogs() -> None:
    originalStandardOutputFileDescriptor = os.dup(sys.stdout.fileno())
    originalStandardErrorFileDescriptor = os.dup(sys.stderr.fileno())
    devNullFileDescriptor = os.open(os.devnull, os.O_WRONLY)
    os.dup2(devNullFileDescriptor, sys.stdout.fileno())
    os.dup2(devNullFileDescriptor, sys.stderr.fileno())
    sys.stdout = os.fdopen(originalStandardOutputFileDescriptor, "w", closefd=False)
    sys.stderr = os.fdopen(originalStandardErrorFileDescriptor, "w", closefd=False)


def createEngineSelection(litertLM: Any, request: WrapperRequest) -> EngineSelection:
    failures = []
    for backendName in request.backendPreference:
        if backendName == "cpu" and not request.allowCPUFallback:
            failures.append("cpu backend skipped because cpu fallback is disabled")
            continue
        try:
            backend = parseLiteRTBackend(litertLM, backendName)
            engine = litertLM.Engine(request.modelPath, backend=backend)
            return EngineSelection(engine=engine, selectedBackend=backendName)
        except Exception as exception:
            failures.append(f"{backendName}: {exception}")
    raise RuntimeError("failed to initialize LiteRT-LM backend: " + "; ".join(failures))


def parseLiteRTBackend(litertLM: Any, backendName: str) -> Any:
    if backendName == "gpu":
        return litertLM.Backend.GPU
    if backendName == "cpu":
        return litertLM.Backend.CPU
    raise ValueError(f"unsupported LiteRT-LM backend: {backendName}")


def splitConversationMessages(request: WrapperRequest) -> tuple[list[dict[str, Any]], str]:
    messages = request.messages
    if not messages:
        return buildSystemMessages(request), buildStructuredPrompt("", request)

    prefaceMessages = buildSystemMessages(request)
    for message in messages[:-1]:
        prefaceMessages.append(normalizeConversationMessage(message))

    userPrompt = messageText(messages[-1])
    return prefaceMessages, buildStructuredPrompt(userPrompt, request)


def buildSystemMessages(request: WrapperRequest) -> list[dict[str, Any]]:
    if not request.enableConstrainedDecoding:
        return []
    constraintType = request.decodingConstraint.get("type")
    constraintString = request.decodingConstraint.get("constraintString")
    if constraintType != "jsonSchema" or not constraintString:
        return []
    return [
        {
            "role": "system",
            "content": [
                {
                    "type": "text",
                    "text": "Return only valid JSON. Do not wrap the JSON in markdown.",
                }
            ],
        }
    ]


def buildStructuredPrompt(userPrompt: str, request: WrapperRequest) -> str:
    constraintType = request.decodingConstraint.get("type")
    constraintString = request.decodingConstraint.get("constraintString")
    if not request.enableConstrainedDecoding or constraintType != "jsonSchema" or not constraintString:
        return userPrompt
    return (
        userPrompt
        + "\n\nReturn a JSON document that matches this JSON Schema exactly:\n"
        + str(constraintString)
    )


def normalizeConversationMessage(message: dict[str, Any]) -> dict[str, Any]:
    return {
        "role": str(message.get("role", "user")),
        "content": [{"type": "text", "text": messageText(message)}],
    }


def messageText(message: dict[str, Any]) -> str:
    content = message.get("content", "")
    if isinstance(content, str):
        return content
    return json.dumps(content, ensure_ascii=False)


def extractText(modelResponse: dict[str, Any]) -> str:
    content = modelResponse.get("content", [])
    textParts = []
    for item in content:
        if item.get("type") == "text":
            textParts.append(str(item.get("text", "")))
    channels = modelResponse.get("channels", {})
    for channelContent in channels.values():
        textParts.append(str(channelContent))
    text = "".join(textParts).strip()
    if not text:
        raise RuntimeError("LiteRT-LM returned an empty response")
    return text


def normalizeBackendPreference(value: Any) -> list[str]:
    if not isinstance(value, list):
        return ["gpu", "cpu"]
    backendPreference = []
    seenBackendNames = set()
    for item in value:
        backendName = str(item).strip().lower()
        if not backendName or backendName in seenBackendNames:
            continue
        seenBackendNames.add(backendName)
        backendPreference.append(backendName)
    return backendPreference or ["gpu", "cpu"]


def normalizeMessages(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    messages = []
    for item in value:
        if isinstance(item, dict):
            messages.append(item)
    return messages


def requireString(payload: dict[str, Any], key: str) -> str:
    value = payload.get(key)
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{key} is required")
    return value


def writeResponse(response: dict[str, Any]) -> None:
    sys.stdout.write(json.dumps(response, ensure_ascii=False) + "\n")
    sys.stdout.flush()


if __name__ == "__main__":
    raise SystemExit(main())
