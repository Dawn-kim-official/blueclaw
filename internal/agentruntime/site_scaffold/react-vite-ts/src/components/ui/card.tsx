import * as React from "react";
import { cn } from "../../lib/utils";

export function Card({ className, ...properties }: React.ComponentProps<"div">) {
	return <div className={cn("rounded-lg border bg-card text-card-foreground shadow-sm", className)} {...properties} />;
}

export function CardHeader({ className, ...properties }: React.ComponentProps<"div">) {
	return <div className={cn("flex flex-col space-y-1.5 p-6", className)} {...properties} />;
}

export function CardTitle({ className, ...properties }: React.ComponentProps<"h3">) {
	return <h3 className={cn("text-2xl font-semibold leading-none tracking-[0px]", className)} {...properties} />;
}

export function CardDescription({ className, ...properties }: React.ComponentProps<"p">) {
	return <p className={cn("text-sm text-muted-foreground", className)} {...properties} />;
}

export function CardContent({ className, ...properties }: React.ComponentProps<"div">) {
	return <div className={cn("p-6 pt-0", className)} {...properties} />;
}

export function CardFooter({ className, ...properties }: React.ComponentProps<"div">) {
	return <div className={cn("flex items-center p-6 pt-0", className)} {...properties} />;
}
