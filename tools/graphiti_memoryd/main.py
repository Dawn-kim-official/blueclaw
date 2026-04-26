import asyncio
import hashlib
import json
import os
import sys
import traceback
import urllib.error
import urllib.request
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from graphiti_core import Graphiti
from graphiti_core.cross_encoder.client import CrossEncoderClient
from graphiti_core.driver.kuzu_driver import KuzuDriver
from graphiti_core.driver.driver import GraphProvider
from graphiti_core.embedder.client import EmbedderClient, EmbedderConfig
from graphiti_core.graph_queries import get_fulltext_indices
from graphiti_core.llm_client.client import LLMClient
from graphiti_core.llm_client.config import LLMConfig, ModelSize
from graphiti_core.nodes import EpisodeType
import requests_unixsocket


class CapabilityLLMClient(LLMClient):
    def __init__(self, endpoint: str, model: str):
        super().__init__(LLMConfig(api_key="capability", model=model, small_model=model))
        self.endpoint = endpoint.rstrip("/")
        self.model_name = model

    async def _generate_response(
        self,
        messages: list[Any],
        response_model: type[Any] | None = None,
        max_tokens: int = 8192,
        model_size: ModelSize = ModelSize.medium,
    ) -> dict[str, Any]:
        schema = response_model.model_json_schema() if response_model else None
        schema_name = getattr(response_model, "__name__", "graphiti_response") if response_model else "graphiti_response"
        request_document = {
            "model": self.model_name,
            "executionMode": os.environ.get("BLUECLAW_GRAPHITI_EXECUTION_MODE", "auto"),
            "messages": [dump_message(message) for message in messages],
            "structuredOutputSchema": {
                "name": schema_name,
                "document": schema,
                "isStrictlyEnforced": True,
            },
            "maxTokens": max_tokens,
        }
        response_document = await asyncio.to_thread(
            post_json,
            self.endpoint + "/v1/llm/structured",
            request_document,
        )
        content = response_document.get("content", "")
        if isinstance(content, str):
            return json.loads(content)
        if isinstance(content, dict):
            return content
        return {"content": str(content)}


class CapabilityEmbedder(EmbedderClient):
    def __init__(self, endpoint: str):
        self.endpoint = endpoint.rstrip("/")
        self.config = EmbedderConfig()

    async def create(self, input_data: str | list[str] | Any) -> list[float]:
        if not isinstance(input_data, str):
            input_data = json.dumps(input_data, ensure_ascii=False)
        response_document = await asyncio.to_thread(
            post_json,
            self.endpoint + "/v1/embedding/create",
            {"input": input_data},
        )
        embedding = response_document.get("embedding", [])
        return [float(value) for value in embedding]

    async def create_batch(self, input_data_list: list[str]) -> list[list[float]]:
        response_document = await asyncio.to_thread(
            post_json,
            self.endpoint + "/v1/embedding/create",
            {"input": input_data_list},
        )
        embeddings = response_document.get("embeddings", [])
        return [[float(value) for value in embedding] for embedding in embeddings]


class CapabilityReranker(CrossEncoderClient):
    def __init__(self, endpoint: str):
        self.endpoint = endpoint.rstrip("/")

    async def rank(self, query: str, passages: list[str]) -> list[tuple[str, float]]:
        if len(passages) == 0:
            return []
        try:
            response_document = await asyncio.to_thread(
                post_json,
                self.endpoint + "/v1/rerank/score",
                {"query": query, "passages": passages},
            )
        except Exception:
            return [(passage, 1.0 / (index + 1)) for index, passage in enumerate(passages)]
        ranked_passages = response_document.get("rankedPassages", [])
        return [(item.get("passage", ""), float(item.get("score", 0))) for item in ranked_passages]


def dump_message(message: Any) -> dict[str, str]:
    if isinstance(message, dict):
        return {
            "role": str(message.get("role", "user")),
            "content": str(message.get("content", "")),
        }
    if hasattr(message, "model_dump"):
        document = message.model_dump()
        return {
            "role": str(document.get("role", "user")),
            "content": str(document.get("content", "")),
        }
    return {
        "role": str(getattr(message, "role", "user")),
        "content": str(getattr(message, "content", message)),
    }


class GraphitiMemoryService:
    def __init__(self):
        capability_endpoint = os.environ.get("INTERNKIM_CAPABILITY_ENDPOINT", "http+unix://%2Frun%2Finternkim%2Fcapability.sock")
        kuzu_path = os.environ.get("BLUECLAW_GRAPHITI_KUZU_PATH", "/workspace/.blueclaw/graphiti/kuzu")
        model = os.environ.get("BLUECLAW_GRAPHITI_MODEL", "google/gemini-3-flash-preview")
        os.makedirs(os.path.dirname(kuzu_path), exist_ok=True)
        graph_driver = KuzuDriver(db=kuzu_path)
        graph_driver._database = ""
        asyncio.run(ensure_kuzu_fulltext_indexes(graph_driver))
        self.graphiti = Graphiti(
            graph_driver=graph_driver,
            llm_client=CapabilityLLMClient(capability_endpoint, model),
            embedder=CapabilityEmbedder(capability_endpoint),
            cross_encoder=CapabilityReranker(capability_endpoint),
        )
        asyncio.run(self.graphiti.build_indices_and_constraints())

    async def add_episode(self, request_document: dict[str, Any]) -> dict[str, Any]:
        episode_id = request_document["episodeID"]
        prompt = request_document["prompt"]
        sender_person_id = request_document["senderPersonID"]
        occurred_at = parse_datetime(request_document.get("occurredAt"))
        episode_body = sender_person_id + ": " + prompt
        source_reference = request_document.get("sourceReference", "")
        namespaces = request_document.get("namespaces", [])
        for namespace in namespaces:
            namespace_id = namespace["namespaceID"]
            await self.graphiti.add_episode(
                name=graphiti_group_id(episode_id + ":" + namespace_id),
                episode_body=episode_body,
                source=EpisodeType.message,
                source_description=source_reference,
                reference_time=occurred_at,
                group_id=graphiti_group_id(namespace_id),
            )
        return {"episodeID": episode_id, "namespaceCount": len(namespaces)}

    async def search(self, request_document: dict[str, Any]) -> dict[str, Any]:
        query = request_document.get("Query") or request_document.get("query") or ""
        limit = int(request_document.get("Limit") or request_document.get("limit") or 12)
        namespaces = request_document.get("Namespaces") or request_document.get("namespaces") or []
        facts: list[dict[str, Any]] = []
        for namespace in namespaces:
            namespace_id = namespace["namespaceID"]
            results = await self.graphiti.search(query=query, group_ids=[graphiti_group_id(namespace_id)], num_results=limit)
            for result in results:
                facts.append(
                    {
                        "factID": getattr(result, "uuid", ""),
                        "scopeType": namespace.get("scopeType", ""),
                        "namespaceID": namespace_id,
                        "content": getattr(result, "fact", ""),
                        "score": float(getattr(result, "score", 0) or 0),
                        "sourceEpisodeID": getattr(result, "source_node_uuid", ""),
                        "validAt": serialize_datetime(getattr(result, "valid_at", None)),
                        "securityLevelRank": namespace.get("securityLevelRank", 0),
                        "requiredClasses": namespace.get("requiredClasses", []),
                    }
                )
        return {"facts": facts[:limit]}


def graphiti_group_id(namespace_id: str) -> str:
    digest = hashlib.sha256(namespace_id.encode("utf-8")).hexdigest()[:24]
    return "bc_" + digest


async def ensure_kuzu_fulltext_indexes(graph_driver: KuzuDriver):
    for query in get_fulltext_indices(GraphProvider.KUZU):
        try:
            await graph_driver.execute_query(query)
        except Exception as error:
            if "already exists" not in str(error).lower():
                raise


def post_json(url: str, request_document: dict[str, Any]) -> dict[str, Any]:
    if url.startswith("http+unix://"):
        session = requests_unixsocket.Session()
        response = session.post(url, json=request_document, timeout=30)
        if response.status_code >= 400:
            raise RuntimeError(response.text)
        if response.text.strip() == "":
            return {}
        return response.json()

    request_body = json.dumps(request_document).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=request_body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read().decode("utf-8")
    except urllib.error.HTTPError as error:
        body = error.read().decode("utf-8")
        raise RuntimeError(body)
    if body.strip() == "":
        return {}
    return json.loads(body)


def parse_datetime(value: str | None) -> datetime:
    if not value:
        return datetime.now(timezone.utc)
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def serialize_datetime(value: Any) -> str:
    if isinstance(value, datetime):
        return value.astimezone(timezone.utc).isoformat()
    return ""


class RequestHandler(BaseHTTPRequestHandler):
    service: GraphitiMemoryService

    def do_GET(self):
        if self.path == "/health":
            self.write_json(200, {"status": "ok"})
            return
        self.write_json(404, {"error": "not found"})

    def do_POST(self):
        try:
            request_document = self.read_json()
            if self.path == "/v1/episodes":
                response_document = asyncio.run(self.service.add_episode(request_document))
            elif self.path == "/v1/search":
                response_document = asyncio.run(self.service.search(request_document))
            else:
                self.write_json(404, {"error": "not found"})
                return
            self.write_json(200, response_document)
        except Exception as error:
            traceback.print_exc()
            self.write_json(500, {"error": str(error), "traceback": traceback.format_exc()})

    def read_json(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length).decode("utf-8")
        if body.strip() == "":
            return {}
        return json.loads(body)

    def write_json(self, status_code: int, document: dict[str, Any]):
        body = json.dumps(document).encode("utf-8")
        self.send_response(status_code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *arguments):
        return


def main():
    listen_address = os.environ.get("BLUECLAW_GRAPHITI_LISTEN_ADDRESS", "127.0.0.1")
    listen_port = int(os.environ.get("BLUECLAW_GRAPHITI_PORT", "7791"))
    try:
        RequestHandler.service = GraphitiMemoryService()
    except Exception as error:
        print(str(error), file=sys.stderr)
        raise
    server = ThreadingHTTPServer((listen_address, listen_port), RequestHandler)
    server.serve_forever()
