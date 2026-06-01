import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

const buttonVariants = cva(
	"inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
	{
		variants: {
			variant: {
				default: "bg-primary text-primary-foreground hover:bg-secondary",
				secondary: "bg-secondary text-secondary-foreground hover:bg-secondary/90",
				outline: "border border-input bg-background hover:bg-muted",
				ghost: "hover:bg-muted",
				destructive: "bg-destructive text-destructive-foreground hover:bg-destructive/90",
			},
			size: {
				default: "h-10 px-4 py-2",
				sm: "h-9 rounded-md px-3",
				lg: "h-11 rounded-md px-8",
				icon: "h-10 w-10",
			},
		},
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	},
);

export function Button({
	className,
	variant,
	size,
	...properties
}: React.ComponentProps<"button"> & VariantProps<typeof buttonVariants>) {
	return <button className={cn(buttonVariants({ variant, size }), className)} {...properties} />;
}
