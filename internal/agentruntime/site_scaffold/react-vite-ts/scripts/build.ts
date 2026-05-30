import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";

type Command = {
	name: string;
	arguments: string[];
};

type QualityIssue = {
	severity: "blocking" | "warning";
	category: string;
	target: string;
	message: string;
};

async function runCommand(command: Command): Promise<void> {
	const commandProcess = Bun.spawn([command.name, ...command.arguments], {
		stdout: "inherit",
		stderr: "inherit",
	});
	const exitCode = await commandProcess.exited;
	if (exitCode !== 0) {
		throw new Error(command.name + " " + command.arguments.join(" ") + " failed with exit code " + exitCode);
	}
}

function readSource(path: string): string {
	if (!existsSync(path)) return "";
	return readFileSync(path, "utf8");
}

function sourceContainsAny(source: string, values: string[]): boolean {
	return values.some((value) => source.includes(value));
}

function collectQualityIssues(): QualityIssue[] {
	const appSource = readSource("src/App.tsx");
	const styleSource = readSource("src/index.css");
	const issues: QualityIssue[] = [];
	if (!existsSync("src/prototype-data.ts")) {
		issues.push({
			severity: "blocking",
			category: "contentModel",
			target: "src/prototype-data.ts",
			message: "Create domain-specific prototype data before building the site.",
		});
	}
	if (sourceContainsAny(appSource + styleSource, [
		"INTERNKIM_SITE_STARTER_REPLACE_ME",
		"InternKim React prototype",
		"Beautiful default scaffold",
		"Replace this starter",
		"workflowItems",
	])) {
		issues.push({
			severity: "blocking",
			category: "templateSmell",
			target: "src/App.tsx",
			message: "Replace the scaffold starter instead of editing its copy or card-grid structure.",
		});
	}
	return issues;
}

function writeBuildQuality(issues: QualityIssue[]): void {
	mkdirSync("../.internkim", { recursive: true });
	writeFileSync("../.internkim/build-quality.json", JSON.stringify({
		generatedAt: new Date().toISOString(),
		blockingIssueCount: issues.filter((issue) => issue.severity === "blocking").length,
		issues,
	}, null, 2) + "\n");
}

if (!existsSync("../DESIGN.md")) {
	throw new Error("DESIGN.md is required at the site workspace root");
}

const qualityIssues = collectQualityIssues();
if (qualityIssues.some((issue) => issue.severity === "blocking")) {
	writeBuildQuality(qualityIssues);
	throw new Error("site quality gate failed; see ../.internkim/build-quality.json");
}

if (!existsSync("node_modules")) {
	await runCommand({ name: "bun", arguments: ["install"] });
}

await runCommand({ name: "bunx", arguments: ["@google/design.md", "lint", "../DESIGN.md"] });
await runCommand({ name: "bunx", arguments: ["vite", "build"] });
writeBuildQuality(qualityIssues);
