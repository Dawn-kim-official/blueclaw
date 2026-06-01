import * as React from "react";
import { cn } from "../../lib/utils";

export function Table({ className, ...properties }: React.ComponentProps<"table">) {
	return (
		<div className="w-full overflow-auto">
			<table className={cn("w-full caption-bottom text-sm", className)} {...properties} />
		</div>
	);
}

export function TableHeader({ className, ...properties }: React.ComponentProps<"thead">) {
	return <thead className={cn("[&_tr]:border-b", className)} {...properties} />;
}

export function TableBody({ className, ...properties }: React.ComponentProps<"tbody">) {
	return <tbody className={cn("[&_tr:last-child]:border-0", className)} {...properties} />;
}

export function TableRow({ className, ...properties }: React.ComponentProps<"tr">) {
	return <tr className={cn("border-b transition-colors hover:bg-muted/60", className)} {...properties} />;
}

export function TableHead({ className, ...properties }: React.ComponentProps<"th">) {
	return <th className={cn("h-10 px-2 text-left align-middle font-semibold text-muted-foreground", className)} {...properties} />;
}

export function TableCell({ className, ...properties }: React.ComponentProps<"td">) {
	return <td className={cn("p-2 align-middle", className)} {...properties} />;
}

export function TableCaption({ className, ...properties }: React.ComponentProps<"caption">) {
	return <caption className={cn("mt-4 text-sm text-muted-foreground", className)} {...properties} />;
}
