import * as React from "react";
import { cn } from "../../lib/utils";

export function Separator({ className, orientation = "horizontal", ...properties }: React.ComponentProps<"div"> & { orientation?: "horizontal" | "vertical" }) {
	return (
		<div
			className={cn(orientation === "horizontal" ? "h-px w-full" : "h-full w-px", "shrink-0 bg-border", className)}
			{...properties}
		/>
	);
}
