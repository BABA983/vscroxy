export const enum EncodingMode {

	/**
	 * Instructs the encoding support to encode the object with the provided encoding
	 */
	Encode,

	/**
	 * Instructs the encoding support to decode the object with the provided encoding
	 */
	Decode
}

export interface IEncodingSupport {

	/**
	 * Gets the encoding of the object if known.
	 */
	getEncoding(): string | undefined;

	/**
	 * Sets the encoding for the object for saving.
	 */
	setEncoding(encoding: string, mode: EncodingMode): Promise<void>;
}

export interface ILanguageSupport {

	/**
	 * Sets the language id of the object.
	 */
	setLanguageId(languageId: string, source?: string): void;
}
