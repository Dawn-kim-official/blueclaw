import * as React from "react";
import { cn } from "../../lib/utils";

export function Label({ className, ...properties }: React.ComponentProps<"label">) {
	return <label className={cn("text-sm font-semibold leading-none", className)} {...properties} />;
}
