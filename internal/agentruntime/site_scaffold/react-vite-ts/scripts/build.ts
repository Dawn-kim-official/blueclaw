import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const rootPath = process.cwd();
const sourcePath = join(rootPath, "src");
const distPath = join(rootPath, "dist");

async function readOptionalFile(path: string, fallback: string): Promise<string> {
  try {
    return await readFile(path, "utf8");
  } catch (error) {
    if (error && typeof error === "object" && "code" in error && error.code === "ENOENT") {
      return fallback;
    }
    throw error;
  }
}

const template = await readFile(join(rootPath, "index.html"), "utf8");
const body = await readOptionalFile(join(sourcePath, "content.html"), "<main></main>\n");
const styles = await readOptionalFile(join(sourcePath, "styles.css"), "");
const script = await readOptionalFile(join(sourcePath, "script.js"), "");

const document = template
  .replace("__SITE_STYLES__", styles)
  .replace("__SITE_BODY__", body)
  .replace("__SITE_SCRIPT__", script);

await mkdir(distPath, { recursive: true });
await writeFile(join(distPath, "index.html"), document, "utf8");
