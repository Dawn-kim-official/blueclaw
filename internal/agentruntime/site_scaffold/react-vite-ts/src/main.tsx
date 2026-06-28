import { renderApp } from "./App";

const rootElement = document.getElementById("root");

if (!rootElement) {
	throw new Error("Site root element is missing");
}

renderApp(rootElement);
