import { app, BrowserWindow, session, WebFrameMain } from 'electron';
import { isSigPipeError, onUnexpectedError, setUnexpectedErrorHandler } from '../../base/common/errors.js';
import { Event } from '../../base/common/event.js';
import { Disposable } from '../../base/common/lifecycle.js';
import { Schemas, VSCODE_AUTHORITY } from '../../base/common/network.js';
import { IProcessEnvironment, isMacintosh } from '../../base/common/platform.js';
import { URI } from '../../base/common/uri.js';
import { validatedIpcMain } from '../../base/parts/ipc/electron-main/ipcMain.js';
import { NativeParsedArgs } from '../../platform/environment/common/argv.js';
import { IEnvironmentMainService } from '../../platform/environment/electron-main/environmentMainService.js';
import { isLaunchedFromCli } from '../../platform/environment/node/argvHelper.js';
import { SyncDescriptor } from '../../platform/instantiation/common/descriptors.js';
import { IInstantiationService, ServicesAccessor } from '../../platform/instantiation/common/instantiation.js';
import { ServiceCollection } from '../../platform/instantiation/common/serviceCollection.js';
import { ILifecycleMainService, LifecycleMainPhase } from '../../platform/lifecycle/electron-main/lifecycleMainService.js';
import { ILogService } from '../../platform/log/common/log.js';
import { IWindowsMainService, OpenContext } from '../../platform/windows/electron-main/windows.js';
import { WindowsMainService } from '../../platform/windows/electron-main/windowsMainService.js';

export class ProxyApplication extends Disposable {

	private windowsMainService: IWindowsMainService | undefined;

	constructor(
		private readonly userEnv: IProcessEnvironment,
		@ILogService private readonly logService: ILogService,
		@IEnvironmentMainService private readonly environmentMainService: IEnvironmentMainService,
		@ILifecycleMainService private readonly lifecycleMainService: ILifecycleMainService,
		@IInstantiationService private readonly mainInstantiationService: IInstantiationService,
	) {
		super();

		this.configureSession();
		this.registerListeners();
	}

	private configureSession(): void {

		//#region Security related measures (https://electronjs.org/docs/tutorial/security)
		//
		// !!! DO NOT CHANGE without consulting the documentation !!!
		//

		const isUrlFromWindow = (requestingUrl?: string | undefined) => requestingUrl?.startsWith(`${Schemas.vscodeFileResource}://${VSCODE_AUTHORITY}`);
		const isUrlFromWebview = (requestingUrl: string | undefined) => requestingUrl?.startsWith(`${Schemas.vscodeWebview}://`);

		const allowedPermissionsInWebview = new Set([
			'clipboard-read',
			'clipboard-sanitized-write',
		]);

		const allowedPermissionsInCore = new Set([
			'media',
			'local-fonts',
		]);

		session.defaultSession.setPermissionRequestHandler((_webContents, permission, callback, details) => {
			if (isUrlFromWebview(details.requestingUrl)) {
				return callback(allowedPermissionsInWebview.has(permission));
			}
			if (isUrlFromWindow(details.requestingUrl)) {
				return callback(allowedPermissionsInCore.has(permission));
			}
			return callback(false);
		});

		session.defaultSession.setPermissionCheckHandler((_webContents, permission, _origin, details) => {
			if (isUrlFromWebview(details.requestingUrl)) {
				return allowedPermissionsInWebview.has(permission);
			}
			if (isUrlFromWindow(details.requestingUrl)) {
				return allowedPermissionsInCore.has(permission);
			}
			return false;
		});

		//#endregion

		//#region Request filtering

		// Block all SVG requests from unsupported origins
		const supportedSvgSchemes = new Set([Schemas.file, Schemas.vscodeFileResource, Schemas.vscodeRemoteResource, Schemas.vscodeManagedRemoteResource, 'devtools']);

		// But allow them if they are made from inside an webview
		const isSafeFrame = (requestFrame: WebFrameMain | undefined): boolean => {
			for (let frame: WebFrameMain | null | undefined = requestFrame; frame; frame = frame.parent) {
				if (frame.url.startsWith(`${Schemas.vscodeWebview}://`)) {
					return true;
				}
			}
			return false;
		};

		const isSvgRequestFromSafeContext = (details: Electron.OnBeforeRequestListenerDetails | Electron.OnHeadersReceivedListenerDetails): boolean => {
			return details.resourceType === 'xhr' || isSafeFrame(details.frame);
		};

		const isAllowedVsCodeFileRequest = (details: Electron.OnBeforeRequestListenerDetails) => {
			const frame = details.frame;
			if (!frame || !this.windowsMainService) {
				return false;
			}

			// Check to see if the request comes from one of the main windows (or shared process) and not from embedded content
			const windows = BrowserWindow.getAllWindows();
			for (const window of windows) {
				if (frame.processId === window.webContents.mainFrame.processId) {
					return true;
				}
			}

			return false;
		};

		const isAllowedWebviewRequest = (uri: URI, details: Electron.OnBeforeRequestListenerDetails): boolean => {
			if (uri.path !== '/index.html') {
				return true; // Only restrict top level page of webviews: index.html
			}

			const frame = details.frame;
			if (!frame || !this.windowsMainService) {
				return false;
			}

			// Check to see if the request comes from one of the main editor windows.
			for (const window of this.windowsMainService.getWindows()) {
				if (window.win) {
					if (frame.processId === window.win.webContents.mainFrame.processId) {
						return true;
					}
				}
			}

			return false;
		};

		session.defaultSession.webRequest.onBeforeRequest((details, callback) => {
			const uri = URI.parse(details.url);
			if (uri.scheme === Schemas.vscodeWebview) {
				if (!isAllowedWebviewRequest(uri, details)) {
					this.logService.error('Blocked vscode-webview request', details.url);
					return callback({ cancel: true });
				}
			}

			if (uri.scheme === Schemas.vscodeFileResource) {
				if (!isAllowedVsCodeFileRequest(details)) {
					this.logService.error('Blocked vscode-file request', details.url);
					return callback({ cancel: true });
				}
			}

			// Block most svgs
			if (uri.path.endsWith('.svg')) {
				const isSafeResourceUrl = supportedSvgSchemes.has(uri.scheme);
				if (!isSafeResourceUrl) {
					return callback({ cancel: !isSvgRequestFromSafeContext(details) });
				}
			}

			return callback({ cancel: false });
		});

		// Configure SVG header content type properly
		// https://github.com/microsoft/vscode/issues/97564
		session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
			const responseHeaders = details.responseHeaders as Record<string, (string) | (string[])>;
			const contentTypes = (responseHeaders['content-type'] || responseHeaders['Content-Type']);

			if (contentTypes && Array.isArray(contentTypes)) {
				const uri = URI.parse(details.url);
				if (uri.path.endsWith('.svg')) {
					if (supportedSvgSchemes.has(uri.scheme)) {
						responseHeaders['Content-Type'] = ['image/svg+xml'];

						return callback({ cancel: false, responseHeaders });
					}
				}

				// remote extension schemes have the following format
				// http://127.0.0.1:<port>/vscode-remote-resource?path=
				if (!uri.path.endsWith(Schemas.vscodeRemoteResource) && contentTypes.some(contentType => contentType.toLowerCase().includes('image/svg'))) {
					return callback({ cancel: !isSvgRequestFromSafeContext(details) });
				}
			}

			return callback({ cancel: false });
		});

		//#endregion

		//#region Allow CORS for the PRSS CDN

		// https://github.com/microsoft/vscode-remote-release/issues/9246
		session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
			if (details.url.startsWith('https://vscode.download.prss.microsoft.com/')) {
				const responseHeaders = details.responseHeaders ?? Object.create(null);

				if (responseHeaders['Access-Control-Allow-Origin'] === undefined) {
					responseHeaders['Access-Control-Allow-Origin'] = ['*'];
					return callback({ cancel: false, responseHeaders });
				}
			}

			return callback({ cancel: false });
		});

		//#endregion

		// TODO: Maybe we should config this after whistle process is ready through whistleProcessLifecycleService
		// region allow whistle CORS
		session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
			if (details.url.startsWith('http://127.0.0.1:8899')) {
				const responseHeaders = details.responseHeaders ?? Object.create(null);

				if (responseHeaders['Access-Control-Allow-Origin'] === undefined) {
					responseHeaders['Access-Control-Allow-Origin'] = ['*'];
					return callback({ cancel: false, responseHeaders });
				}
			}

			return callback({ cancel: false });
		});
		// endregion
	}

	private registerListeners(): void {
		// We handle uncaught exceptions here to prevent electron from opening a dialog to the user
		setUnexpectedErrorHandler(error => this.onUnexpectedError(error));
		process.on('uncaughtException', error => {
			if (!isSigPipeError(error)) {
				onUnexpectedError(error);
			}
		});
		process.on('unhandledRejection', (reason: unknown) => onUnexpectedError(reason));

		// Dispose on shutdown
		Event.once(this.lifecycleMainService.onWillShutdown)(() => this.dispose());

		// TODO: Contextmenu via IPC support
		// registerContextMenuListener();

		// Accessibility change event
		app.on('accessibility-support-changed', (event, accessibilitySupportEnabled) => {
			this.windowsMainService?.sendToAll('vscode:accessibilitySupportChanged', accessibilitySupportEnabled);
		});

		// macOS dock activate
		app.on('activate', async (event, hasVisibleWindows) => {
			this.logService.trace('app#activate');

			// Mac only event: open new window when we get activated
			if (!hasVisibleWindows) {
				await this.windowsMainService?.openEmptyWindow({ context: OpenContext.DOCK });
			}
		});

		//#region Security related measures (https://electronjs.org/docs/tutorial/security)
		//
		// !!! DO NOT CHANGE without consulting the documentation !!!
		//
		app.on('web-contents-created', (event, contents) => {

			// Block any in-page navigation
			contents.on('will-navigate', event => {
				this.logService.error('webContents#will-navigate: Prevented webcontent navigation');

				event.preventDefault();
			});

			// All Windows: only allow about:blank auxiliary windows to open
			// For all other URLs, delegate to the OS.
			contents.setWindowOpenHandler(details => {

				// TODO: about:blank windows can open as window with our default options
				if (details.url === 'about:blank') {
					this.logService.trace('[aux window] webContents#setWindowOpenHandler: Denying auxiliary window to open on about:blank');

					return {
						action: 'deny',
					};
				}

				// Any other URL: delegate to OS
				else {
					this.logService.trace(`webContents#setWindowOpenHandler: Prevented opening window with URL ${details.url}}`);

					// TODO: nativeHost
					// this.nativeHostMainService?.openExternal(undefined, details.url);

					return { action: 'deny' };
				}
			});
		});

		//#endregion

		//#region Bootstrap IPC Handlers

		validatedIpcMain.handle('vscode:fetchShellEnv', event => {

			// Prefer to use the args and env from the target window
			// when resolving the shell env. It is possible that
			// a first window was opened from the UI but a second
			// from the CLI and that has implications for whether to
			// resolve the shell environment or not.
			//
			// Window can be undefined for e.g. the shared process
			// that is not part of our windows registry!
			const window = this.windowsMainService?.getWindowByWebContents(event.sender); // Note: this can be `undefined` for the shared process
			let args: NativeParsedArgs;
			let env: IProcessEnvironment;
			if (window?.config) {
				args = window.config;
				env = { ...process.env, ...window.config.userEnv };
			} else {
				args = this.environmentMainService.args;
				env = process.env;
			}

			// TODO: Resolve shell env
			return Promise.resolve();
			// return this.resolveShellEnvironment(args, env, false);
		});

		validatedIpcMain.on('vscode:toggleDevTools', event => event.sender.toggleDevTools());
		validatedIpcMain.on('vscode:openDevTools', event => event.sender.openDevTools());

		validatedIpcMain.on('vscode:reloadWindow', event => event.sender.reload());

		validatedIpcMain.handle('vscode:notifyZoomLevel', async (event, zoomLevel: number | undefined) => {
			const window = this.windowsMainService?.getWindowByWebContents(event.sender);
			if (window) {
				window.notifyZoomLevel(zoomLevel);
			}
		});

		//#endregion
	}

	private onUnexpectedError(error: Error): void {
		if (error) {

			// take only the message and stack property
			const friendlyError = {
				message: `[uncaught exception in main]: ${error.message}`,
				stack: error.stack
			};

			// TODO: handle on client side
			// this.windowsMainService?.sendToFocused('vscode:reportError', JSON.stringify(friendlyError));
		}

		this.logService.error(`[uncaught exception in main]: ${error}`);
		if (error.stack) {
			this.logService.error(error.stack);
		}
	}

	async startup(): Promise<void> {
		this.logService.debug('Starting VS Croxy');
		this.logService.debug(`from: ${this.environmentMainService.appRoot}`);
		this.logService.debug('args:', this.environmentMainService.args);

		// Services
		const appInstantiationService = await this.initServices();

		// Signal phase: ready - before opening first window
		this.lifecycleMainService.phase = LifecycleMainPhase.Ready;

		// Open Windows
		await appInstantiationService.invokeFunction(accessor => this.openFirstWindow(accessor));

		// Signal phase: after window open
		this.lifecycleMainService.phase = LifecycleMainPhase.AfterWindowOpen;

		this.afterWindowOpen();
	}

	private async initServices() {
		const services = new ServiceCollection();

		// Windows
		services.set(IWindowsMainService, new SyncDescriptor(WindowsMainService, [this.userEnv], false));

		return this.mainInstantiationService.createChild(services);
	}

	private openFirstWindow(accessor: ServicesAccessor) {
		const windowsMainService = (this.windowsMainService = accessor.get(IWindowsMainService));
		const context = isLaunchedFromCli(process.env) ? OpenContext.CLI : OpenContext.DESKTOP;
		const args = this.environmentMainService.args;

		return windowsMainService.open({
			context,
			cli: args,
			forceNewWindow: true,
			forceEmpty: true,
			initialStartup: true,
		});
	}

	private afterWindowOpen(): void {

		// Start to fetch shell environment (if needed) after window has opened
		// Since this operation can take a long time, we want to warm it up while
		// the window is opening.
		// We also show an error to the user in case this fails.
		this.resolveShellEnvironment(this.environmentMainService.args, process.env, true);

		// Crash reporter
		this.updateCrashReporterEnablement();

		// macOS: rosetta translation warning
		if (isMacintosh && app.runningUnderARM64Translation) {
			this.windowsMainService?.sendToFocused('vscode:showTranslatedBuildWarning');
		}
	}

	private async resolveShellEnvironment(args: NativeParsedArgs, env: IProcessEnvironment, notifyOnError: boolean): Promise<typeof process.env> {
		// TODO
		return {};
	}


	// TODO
	private async updateCrashReporterEnablement(): Promise<void> {
	}

}
