export type ManagedChannelSpec = {
	name: string;
	displayName?: string;
	description?: string;
	topic?: string;
};

export type ManagedChannel = {
	channelID: string;
	replyTargetID: string;
	created: boolean;
};

export type ChannelProvisioningAdapter = {
	ensureChannel(spec: ManagedChannelSpec): Promise<ManagedChannel>;
};

export function canonicalChannelName(name: string): string {
	return name.replace(/^[#\s]+/, "").trimEnd();
}

export function supportsChannelProvisioning(adapter: object): adapter is ChannelProvisioningAdapter {
	return typeof (adapter as ChannelProvisioningAdapter).ensureChannel === "function";
}
