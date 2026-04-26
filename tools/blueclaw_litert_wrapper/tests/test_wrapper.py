import json
import unittest
from importlib import import_module

wrapperModule = import_module("blueclaw_litert_wrapper.main")


class FakeBackend:
    GPU = "gpu"
    CPU = "cpu"


class FakeLogSeverity:
    ERROR = "error"


class FakeLiteRTLM:
    Backend = FakeBackend
    LogSeverity = FakeLogSeverity

    def __init__(self):
        self.createdEngines = []

    def set_min_log_severity(self, severity):
        self.severity = severity

    def Engine(self, modelPath, backend):
        engine = FakeEngine(modelPath, backend)
        self.createdEngines.append(engine)
        return engine


class FakeEngine:
    def __init__(self, modelPath, backend):
        self.modelPath = modelPath
        self.backend = backend

    def __enter__(self):
        return self

    def __exit__(self, exceptionType, exceptionValue, traceback):
        return None

    def create_conversation(self, messages=None):
        return FakeConversation(messages or [])


class FakeConversation:
    def __init__(self, messages):
        self.messages = messages

    def __enter__(self):
        return self

    def __exit__(self, exceptionType, exceptionValue, traceback):
        return None

    def send_message(self, message):
        return {
            "role": "assistant",
            "content": [{"type": "text", "text": '{"reply":"안녕하세요"}'}],
        }


class FailingGPUFakeLiteRTLM(FakeLiteRTLM):
    def Engine(self, modelPath, backend):
        if backend == FakeBackend.GPU:
            raise RuntimeError("gpu unavailable")
        return super().Engine(modelPath, backend)


class WrapperTest(unittest.TestCase):
    def test_generates_response_with_gpu_backend(self):
        request = wrapperModule.parseRequest(json.dumps(buildRequest(["gpu"], False)))
        fakeLiteRTLM = FakeLiteRTLM()

        originalImport = wrapperModule.importLiteRTLM
        wrapperModule.importLiteRTLM = lambda: fakeLiteRTLM
        try:
            response = wrapperModule.generateResponse(request)
        finally:
            wrapperModule.importLiteRTLM = originalImport

        self.assertEqual(response["content"], '{"reply":"안녕하세요"}')
        self.assertEqual(response["selectedBackend"], "gpu")

    def test_rejects_cpu_fallback_when_disabled(self):
        request = wrapperModule.parseRequest(json.dumps(buildRequest(["gpu", "cpu"], False)))
        fakeLiteRTLM = FailingGPUFakeLiteRTLM()

        originalImport = wrapperModule.importLiteRTLM
        wrapperModule.importLiteRTLM = lambda: fakeLiteRTLM
        try:
            with self.assertRaises(RuntimeError):
                wrapperModule.generateResponse(request)
        finally:
            wrapperModule.importLiteRTLM = originalImport

    def test_allows_cpu_fallback_when_enabled(self):
        request = wrapperModule.parseRequest(json.dumps(buildRequest(["gpu", "cpu"], True)))
        fakeLiteRTLM = FailingGPUFakeLiteRTLM()

        originalImport = wrapperModule.importLiteRTLM
        wrapperModule.importLiteRTLM = lambda: fakeLiteRTLM
        try:
            response = wrapperModule.generateResponse(request)
        finally:
            wrapperModule.importLiteRTLM = originalImport

        self.assertEqual(response["selectedBackend"], "cpu")


def buildRequest(backendPreference, allowCPUFallback):
    return {
        "modelPath": "/workspace/.blueclaw/models/gemma-4-E4B-it.litertlm",
        "backendPreference": backendPreference,
        "allowCPUFallback": allowCPUFallback,
        "messages": [{"role": "user", "content": "안녕"}],
        "enableConstrainedDecoding": True,
        "constraintProvider": "llguidance",
        "decodingConstraint": {
            "type": "jsonSchema",
            "constraintString": '{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}',
        },
    }


if __name__ == "__main__":
    unittest.main()
