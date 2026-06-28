import { prototypeData } from "./prototype-data";

const starterMarker = "INTERNKIM_SITE_STARTER_REPLACE_ME";

export function renderApp(rootElement: HTMLElement): void {
	rootElement.innerHTML = `
		<main data-starter-marker="${starterMarker}" class="app-shell">
			<section class="starter-panel">
				<p class="eyebrow">Dependency-free site scaffold</p>
				<h1>Replace this starter with the requested site.</h1>
				<p class="lede">
					InternKim site prototype loaded. Use source-backed data, stable controls, and responsive CSS before publishing.
				</p>
				<div class="starter-grid">
					<div>
						<span class="label">Title</span>
						<strong>${escapeHTML(prototypeData.title)}</strong>
					</div>
					<div>
						<span class="label">Status</span>
						<strong>${escapeHTML(prototypeData.status)}</strong>
					</div>
				</div>
				<button class="primary-action" type="button">Replace starter</button>
			</section>
		</main>
	`;
}

function escapeHTML(value: string): string {
	return value
		.replaceAll("&", "&amp;")
		.replaceAll("<", "&lt;")
		.replaceAll(">", "&gt;")
		.replaceAll('"', "&quot;")
		.replaceAll("'", "&#39;");
}
