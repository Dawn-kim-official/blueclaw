import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";

type QualityIssue = {
	severity: "blocking" | "warning";
	category: string;
	target: string;
	message: string;
	suggestedFix: string;
};

function readSource(path: string): string {
	if (!existsSync(path)) return "";
	return readFileSync(path, "utf8");
}

function sourceContainsAny(source: string, values: string[]): boolean {
	return values.some((value) => source.includes(value));
}

function collectDesignQualityIssues(): QualityIssue[] {
	const source = readSource("../DESIGN.md");
	const issueMessages: string[] = [];
	if (!source.startsWith("---\n")) {
		issueMessages.push("DESIGN.md must start with YAML front matter");
		return issueMessages.map(createDesignQualityIssue);
	}
	const frontMatterEnd = source.indexOf("\n---", 4);
	if (frontMatterEnd < 0) {
		issueMessages.push("DESIGN.md front matter must end with ---");
		return issueMessages.map(createDesignQualityIssue);
	}
	const frontMatter = source.slice(4, frontMatterEnd);
	for (const key of ["colors", "typography", "rounded", "spacing", "components"]) {
		if (!new RegExp("^" + key + "\\s*:", "m").test(frontMatter)) {
			issueMessages.push("DESIGN.md front matter is missing " + key);
		}
	}
	const body = source.slice(frontMatterEnd + 4);
	let previousIndex = -1;
	for (const section of ["Overview", "Colors", "Typography", "Layout", "Elevation & Depth", "Shapes", "Components", "Do's and Don'ts"]) {
		const sectionIndex = body.indexOf("## " + section);
		if (sectionIndex < 0) {
			issueMessages.push("DESIGN.md body is missing section: " + section);
			continue;
		}
		if (sectionIndex < previousIndex) {
			issueMessages.push("DESIGN.md section order is wrong at: " + section);
		}
		previousIndex = sectionIndex;
	}
	return issueMessages.map(createDesignQualityIssue);
}

function createDesignQualityIssue(message: string): QualityIssue {
	return {
		severity: "warning",
		category: "designDocument",
		target: "../DESIGN.md",
		message,
		suggestedFix: "Rewrite DESIGN.md using the canonical Stitch front matter and required section order.",
	};
}

function collectQualityIssues(): QualityIssue[] {
	const appSource = readSource("src/App.tsx");
	const mainSource = readSource("src/main.tsx");
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
	if (sourceContainsAny(appSource + mainSource + styleSource, [
		"INTERNKIM_SITE_STARTER_REPLACE_ME",
		"Dependency-free site scaffold",
		"InternKim site prototype loaded",
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
	if (sourceContainsAny(appSource + mainSource, [
		`from "react"`,
		`from 'react'`,
		`from "react-dom`,
		`from 'react-dom`,
		"lucide-react",
		"@radix-ui",
		"class-variance-authority",
		"tailwind-merge",
		"clsx",
	])) {
		issues.push({
			severity: "blocking",
			category: "externalDependency",
			target: "src/App.tsx",
			message: "The managed site scaffold builds offline and does not install npm dependencies.",
			suggestedFix: "Use dependency-free TypeScript, DOM APIs, CSS classes, inline SVG icons, and local data instead of package imports.",
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

function indexTitle(template: string): string {
	const titleMatch = template.match(/<title>(.*?)<\/title>/i);
	return titleMatch?.[1]?.trim() || "InternKim site";
}

function distIndexHTML(template: string): string {
	return [
		"<!doctype html>",
		"<html lang=\"ko\">",
		"<head>",
		"<meta charset=\"UTF-8\" />",
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\" />",
		`<title>${indexTitle(template)}</title>`,
		"<link rel=\"stylesheet\" href=\"./assets/index.css\" />",
		"</head>",
		"<body>",
		"<div id=\"root\"></div>",
		"<script type=\"module\" src=\"./assets/main.js\"></script>",
		"</body>",
		"</html>",
		"",
	].join("\n");
}

async function buildStaticSite(): Promise<void> {
	rmSync("dist", { recursive: true, force: true });
	mkdirSync("dist/assets", { recursive: true });
	const result = await Bun.build({
		entrypoints: ["src/main.tsx"],
		outdir: "dist/assets",
		target: "browser",
		format: "esm",
		minify: false,
		sourcemap: "none",
	});
	if (!result.success) {
		for (const log of result.logs) {
			console.error(log.message);
		}
		throw new Error("Bun static site build failed");
	}
	copyFileSync("src/index.css", "dist/assets/index.css");
	writeFileSync("dist/index.html", distIndexHTML(readSource("index.html")));
}

const qualityIssues = [...collectDesignQualityIssues(), ...collectQualityIssues()];
writeBuildQuality(qualityIssues);
await buildStaticSite();
writeBuildQuality(qualityIssues);
