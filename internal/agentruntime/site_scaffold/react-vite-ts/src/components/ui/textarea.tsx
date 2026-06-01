import * as React from "react";
import { cn } from "../../lib/utils";

export function Textarea({ className, ...properties }: React.ComponentProps<"textarea">) {
	return (
		<textarea
			className={cn("flex min-h-24 w-full rounded-md border border-input bg-card px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50", className)}
			{...properties}
		/>
	);
}
