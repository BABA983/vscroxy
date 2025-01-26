/**
 * A list of command line arguments we support natively.
 */
export interface NativeParsedArgs {
	_: string[];
	version?: boolean;
	verbose?: boolean;
	'open-devtools'?: boolean;
	log?: string[];
	logExtensionHostCommunication?: boolean;
	'extensions-dir'?: string;
	'extensions-download-dir'?: string;
	'builtin-extensions-dir'?: string;
	extensionDevelopmentPath?: string[]; // undefined or array of 1 or more local paths or URIs
	extensionTestsPath?: string; // either a local path or a URI
	extensionDevelopmentKind?: string[];
	extensionEnvironment?: string; // JSON-stringified Record<string, string> object
	'inspect-extensions'?: string;
	'inspect-brk-extensions'?: string;
	debugId?: string;
	'disable-extensions'?: boolean;
	'disable-extension'?: string[]; // undefined or array of 1 or more
	'list-extensions'?: boolean;
	'install-extension'?: string[]; // undefined or array of 1 or more
	'install-builtin-extension'?: string[]; // undefined or array of 1 or more
	'uninstall-extension'?: string[]; // undefined or array of 1 or more
	'update-extensions'?: boolean;
	'locate-extension'?: string[]; // undefined or array of 1 or more
	'enable-proposed-api'?: string[]; // undefined or array of 1 or more
	logsPath?: string;
	locale?: string;
	'user-data-dir'?: string;
	'crash-reporter-id'?: string;
	'crash-reporter-directory'?: string;
	'disable-chromium-sandbox'?: boolean;
	sandbox?: boolean;
	'enable-smoke-test-driver'?: boolean;

	// chromium command line args: https://electronjs.org/docs/all#supported-chrome-command-line-switches
	'no-sandbox'?: boolean;
	'js-flags'?: string;
}
