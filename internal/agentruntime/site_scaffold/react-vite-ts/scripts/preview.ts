import { existsSync } from "node:fs";

function argumentValue(name: string, fallback: string): string {
	const prefix = name + "=";
	for (let index = 0; index < Bun.argv.length; index += 1) {
		const value = Bun.argv[index];
		if (value === name && Bun.argv[index + 1]) return Bun.argv[index + 1];
		if (value.startsWith(prefix)) return value.slice(prefix.length);
	}
	return fallback;
}

function contentType(path: string): string {
	if (path.endsWith(".css")) return "text/css; charset=utf-8";
	if (path.endsWith(".js")) return "text/javascript; charset=utf-8";
	if (path.endsWith(".svg")) return "image/svg+xml";
	if (path.endsWith(".png")) return "image/png";
	if (path.endsWith(".jpg") || path.endsWith(".jpeg")) return "image/jpeg";
	return "text/html; charset=utf-8";
}

function requestedFilePath(pathname: string): string {
	const safePath = pathname.replace(/^\/+/, "").replace(/\.\./g, "");
	if (!safePath || safePath.endsWith("/")) return "dist/index.html";
	return "dist/" + safePath;
}

const host = argumentValue("--host", "127.0.0.1");
const port = Number(argumentValue("--port", "4173"));
if (!existsSync("dist/index.html")) {
	throw new Error("dist/index.html is missing; run bun scripts/build.ts first");
}

const server = Bun.serve({
	hostname: host,
	port,
	async fetch(request) {
		const url = new URL(request.url);
		const path = requestedFilePath(url.pathname);
		const file = Bun.file(existsSync(path) ? path : "dist/index.html");
		return new Response(file, { headers: { "content-type": contentType(path) } });
	},
});

console.log("Preview listening on http://" + host + ":" + server.port);
await new Promise(() => {});
