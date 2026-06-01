import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { Input } from "./components/ui/input";
import { Separator } from "./components/ui/separator";
import { Textarea } from "./components/ui/textarea";

const starterMarker = "INTERNKIM_SITE_STARTER_REPLACE_ME";

function App() {
	return (
		<main data-starter-marker={starterMarker} className="min-h-screen bg-background p-8 text-foreground">
			<section className="mx-auto grid max-w-3xl gap-6">
				<div className="space-y-3">
					<Badge variant="outline">InternKim site scaffold</Badge>
					<h1 className="text-4xl font-bold tracking-[0px]">Replace this starter with the requested site.</h1>
					<p className="max-w-2xl text-muted-foreground">
						Use the local shadcn-style primitives imported from <code>./components/ui/*</code>, then replace this
						starter marker before building and publishing.
					</p>
				</div>
				<Card>
					<CardHeader>
						<CardTitle>Primitive import pattern</CardTitle>
						<CardDescription>Keep config files managed; customize App.tsx, prototype-data.ts, and index.css.</CardDescription>
					</CardHeader>
					<CardContent className="grid gap-4">
						<div className="grid gap-3 sm:grid-cols-2">
							<Input value="Button, Card, Badge, Input" readOnly />
							<Input value="Textarea, Separator, Tabs, Dialog" readOnly />
						</div>
						<Textarea value="Build the real experience here, not a generic feature-card page." readOnly />
						<Separator />
						<div>
							<Button type="button">Replace starter</Button>
						</div>
					</CardContent>
				</Card>
			</section>
		</main>
	);
}

export default App;
