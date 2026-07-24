import { mkdir } from "node:fs/promises";
import path from "node:path";
import type { ChatdConfiguration } from "./configuration.ts";
import type { InputAttachmentDocument } from "./outbound-types.ts";

export async function importAttachmentToDirectory(
	configuration: ChatdConfiguration,
	targetDirectoryPath: string,
	attachment: InputAttachmentDocument,
): Promise<InputAttachmentDocument> {
	if (!attachment.fileID) {
		return { ...attachment, isAvailable: false, errorCode: "missing_file_id" };
	}

	const downloadResponse = await fetchMattermostFile(configuration, attachment.fileID);
	if (!downloadResponse.ok) {
		return {
			...attachment,
			isAvailable: false,
			errorCode: "download_failed",
			message: `mattermost file download returned ${downloadResponse.status}`,
		};
	}

	const fileBytes = new Uint8Array(await downloadResponse.arrayBuffer());
	const filename = attachment.filename?.trim() || attachment.fileID;
	const filePath = path.join(targetDirectoryPath, filename);
	await mkdir(targetDirectoryPath, { recursive: true });
	await Bun.write(filePath, fileBytes);

	return {
		...attachment,
		path: filePath,
		isAvailable: true,
		sizeBytes: fileBytes.byteLength,
		contentType: attachment.contentType ?? downloadResponse.headers.get("content-type") ?? undefined,
	};
}

function fetchMattermostFile(configuration: ChatdConfiguration, fileID: string): Promise<Response> {
	const baseUrl = (configuration.mattermost?.baseURL ?? "").replace(/\/$/, "");
	return fetch(`${baseUrl}/api/v4/files/${fileID}`, {
		headers: { Authorization: `Bearer ${configuration.mattermost?.botToken ?? ""}` },
	});
}
