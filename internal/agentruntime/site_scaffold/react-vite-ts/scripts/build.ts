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
	suggestedFix: string;
};

const canonicalRuntimePATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin";

async function runCommand(command: Command): Promise<void> {
	const commandProcess = Bun.spawn([command.name, ...command.arguments], {
		env: { ...Bun.env, PATH: canonicalRuntimePATH },
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
			suggestedFix: "Add realistic domain data in src/prototype-data.ts and render that data from App.tsx.",
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
			suggestedFix: "Replace starter sections with a domain-specific first screen, real content structure, and non-generic UI flow.",
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
writeBuildQuality(qualityIssues);

if (!existsSync("node_modules")) {
	await runCommand({ name: "bun", arguments: ["install"] });
}

await runCommand({ name: "bun", arguments: ["x", "@google/design.md", "lint", "../DESIGN.md"] });
await runCommand({ name: "bun", arguments: ["x", "vite", "build"] });
writeBuildQuality(qualityIssues);
