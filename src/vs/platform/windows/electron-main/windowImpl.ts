/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import electron, { BrowserWindowConstructorOptions } from 'electron';
import { release } from 'os';
import { DeferredPromise, RunOnceScheduler, timeout } from '../../../base/common/async.js';
import { CancellationToken } from '../../../base/common/cancellation.js';
import { toErrorMessage } from '../../../base/common/errorMessage.js';
import { Emitter, Event } from '../../../base/common/event.js';
import { Disposable } from '../../../base/common/lifecycle.js';
import { FileAccess } from '../../../base/common/network.js';
import { getMarks, mark } from '../../../base/common/performance.js';
import { isBigSurOrNewer, isMacintosh, isWindows } from '../../../base/common/platform.js';
import { localize } from '../../../nls.js';
import { NativeParsedArgs } from '../../environment/common/argv.js';
import { IEnvironmentMainService } from '../../environment/electron-main/environmentMainService.js';
import { isLaunchedFromCli } from '../../environment/node/argvHelper.js';
import { ILifecycleMainService } from '../../lifecycle/electron-main/lifecycleMainService.js';
import { ILogService } from '../../log/common/log.js';
import { IProductService } from '../../product/common/productService.js';
import { IProtocolMainService } from '../../protocol/electron-main/protocol.js';
import { DEFAULT_CUSTOM_TITLEBAR_HEIGHT, INativeWindowConfiguration, MenuBarVisibility, useWindowControlsOverlay } from '../../window/common/window.js';
import { defaultWindowState, IBaseWindow, ILoadEvent, IProxyWindow, IWindowState, LoadReason, WindowError, WindowMode } from '../../window/electron-main/window.js';
import { defaultBrowserWindowOptions, IWindowsMainService, OpenContext, WindowStateValidator } from './windows.js';

import { VSBuffer } from '../../../base/common/buffer.js';
import { IInstantiationService } from '../../instantiation/common/instantiation.js';
import { ILoggerMainService } from '../../log/electron-main/loggerService.js';

export interface IWindowCreationOptions {
	readonly state: IWindowState;
	readonly extensionDevelopmentPath?: string[];
	readonly isExtensionTestHost?: boolean;
}

interface ITouchBarSegment extends electron.SegmentedControlSegment {
	readonly id: string;
}

interface ILoadOptions {
	readonly isReload?: boolean;
	readonly disableExtensions?: boolean;
}

const enum ReadyState {

	/**
	 * This window has not loaded anything yet
	 * and this is the initial state of every
	 * window.
	 */
	NONE,

	/**
	 * This window is navigating, either for the
	 * first time or subsequent times.
	 */
	NAVIGATING,

	/**
	 * This window has finished loading and is ready
	 * to forward IPC requests to the web contents.
	 */
	READY
}

export abstract class BaseWindow extends Disposable implements IBaseWindow {

	//#region Events

	private readonly _onDidClose = this._register(new Emitter<void>());
	readonly onDidClose = this._onDidClose.event;

	private readonly _onDidMaximize = this._register(new Emitter<void>());
	readonly onDidMaximize = this._onDidMaximize.event;

	private readonly _onDidUnmaximize = this._register(new Emitter<void>());
	readonly onDidUnmaximize = this._onDidUnmaximize.event;

	private readonly _onDidTriggerSystemContextMenu = this._register(new Emitter<{ x: number; y: number }>());
	readonly onDidTriggerSystemContextMenu = this._onDidTriggerSystemContextMenu.event;

	private readonly _onDidEnterFullScreen = this._register(new Emitter<void>());
	readonly onDidEnterFullScreen = this._onDidEnterFullScreen.event;

	private readonly _onDidLeaveFullScreen = this._register(new Emitter<void>());
	readonly onDidLeaveFullScreen = this._onDidLeaveFullScreen.event;

	//#endregion

	abstract readonly id: number;

	protected _lastFocusTime = Date.now(); // window is shown on creation so take current time
	get lastFocusTime(): number { return this._lastFocusTime; }

	protected _win: electron.BrowserWindow | null = null;
	get win() { return this._win; }
	protected setWin(win: electron.BrowserWindow, options?: BrowserWindowConstructorOptions): void {
		this._win = win;

		// Window Events
		this._register(Event.fromNodeEventEmitter(win, 'maximize')(() => this._onDidMaximize.fire()));
		this._register(Event.fromNodeEventEmitter(win, 'unmaximize')(() => this._onDidUnmaximize.fire()));
		this._register(Event.fromNodeEventEmitter(win, 'closed')(() => {
			this._onDidClose.fire();

			this.dispose();
		}));
		this._register(Event.fromNodeEventEmitter(win, 'focus')(() => {
			this._lastFocusTime = Date.now();
		}));
		this._register(Event.fromNodeEventEmitter(this._win, 'enter-full-screen')(() => this._onDidEnterFullScreen.fire()));
		this._register(Event.fromNodeEventEmitter(this._win, 'leave-full-screen')(() => this._onDidLeaveFullScreen.fire()));

		// Sheet Offsets
		if (isMacintosh) {
			win.setSheetOffset(isBigSurOrNewer(release()) ? 28 : 22); // offset dialogs by the height of the custom title bar if we have any
		}

		// Update the window controls immediately based on cached or default values
		if (isMacintosh) {
			// TODO: stateService
			this.updateWindowControls({ height: DEFAULT_CUSTOM_TITLEBAR_HEIGHT });
		}

		// Windows Custom System Context Menu
		// See https://github.com/electron/electron/issues/24893
		//
		// The purpose of this is to allow for the context menu in the Windows Title Bar
		//
		// Currently, all mouse events in the title bar are captured by the OS
		// thus we need to capture them here with a window hook specific to Windows
		// and then forward them to the correct window.
		if (isWindows) {
			const WM_INITMENU = 0x0116; // https://docs.microsoft.com/en-us/windows/win32/menurc/wm-initmenu

			// This sets up a listener for the window hook. This is a Windows-only API provided by electron.
			win.hookWindowMessage(WM_INITMENU, () => {
				const [x, y] = win.getPosition();
				const cursorPos = electron.screen.getCursorScreenPoint();
				const cx = cursorPos.x - x;
				const cy = cursorPos.y - y;

				// In some cases, show the default system context menu
				// 1) The mouse position is not within the title bar
				// 2) The mouse position is within the title bar, but over the app icon
				// We do not know the exact title bar height but we make an estimate based on window height
				const shouldTriggerDefaultSystemContextMenu = () => {
					// Use the custom context menu when over the title bar, but not over the app icon
					// The app icon is estimated to be 30px wide
					// The title bar is estimated to be the max of 35px and 15% of the window height
					if (cx > 30 && cy >= 0 && cy <= Math.max(win.getBounds().height * 0.15, 35)) {
						return false;
					}

					return true;
				};

				if (!shouldTriggerDefaultSystemContextMenu()) {

					// This is necessary to make sure the native system context menu does not show up.
					win.setEnabled(false);
					win.setEnabled(true);

					this._onDidTriggerSystemContextMenu.fire({ x: cx, y: cy });
				}

				return 0;
			});
		}

		// Open devtools if instructed from command line args
		if (this.environmentMainService.args['open-devtools'] === true) {
			win.webContents.openDevTools();
		}

		// macOS: Window Fullscreen Transitions
		if (isMacintosh) {
			this._register(this.onDidEnterFullScreen(() => {
				this.joinNativeFullScreenTransition?.complete(true);
			}));

			this._register(this.onDidLeaveFullScreen(() => {
				this.joinNativeFullScreenTransition?.complete(true);
			}));
		}
	}

	constructor(
		protected readonly environmentMainService: IEnvironmentMainService,
		protected readonly logService: ILogService
	) {
		super();
	}

	protected applyState(state: IWindowState, hasMultipleDisplays = electron.screen.getAllDisplays().length > 0): void {

		// TODO@electron (Electron 4 regression): when running on multiple displays where the target display
		// to open the window has a larger resolution than the primary display, the window will not size
		// correctly unless we set the bounds again (https://github.com/microsoft/vscode/issues/74872)
		//
		// Extended to cover Windows as well as Mac (https://github.com/microsoft/vscode/issues/146499)
		//
		// However, when running with native tabs with multiple windows we cannot use this workaround
		// because there is a potential that the new window will be added as native tab instead of being
		// a window on its own. In that case calling setBounds() would cause https://github.com/microsoft/vscode/issues/75830

		if ((isMacintosh || isWindows) && hasMultipleDisplays && (electron.BrowserWindow.getAllWindows().length === 1)) {
			if ([state.width, state.height, state.x, state.y].every(value => typeof value === 'number')) {
				this._win?.setBounds({
					width: state.width,
					height: state.height,
					x: state.x,
					y: state.y
				});
			}
		}

		if (state.mode === WindowMode.Maximized || state.mode === WindowMode.Fullscreen) {

			// this call may or may not show the window, depends
			// on the platform: currently on Windows and Linux will
			// show the window as active. To be on the safe side,
			// we show the window at the end of this block.
			this._win?.maximize();

			if (state.mode === WindowMode.Fullscreen) {
				this.setFullScreen(true, true);
			}

			// to reduce flicker from the default window size
			// to maximize or fullscreen, we only show after
			this._win?.show();
		}
	}

	private representedFilename: string | undefined;

	setRepresentedFilename(filename: string): void {
		if (isMacintosh) {
			this.win?.setRepresentedFilename(filename);
		} else {
			this.representedFilename = filename;
		}
	}

	getRepresentedFilename(): string | undefined {
		if (isMacintosh) {
			return this.win?.getRepresentedFilename();
		}

		return this.representedFilename;
	}

	private documentEdited: boolean | undefined;

	setDocumentEdited(edited: boolean): void {
		if (isMacintosh) {
			this.win?.setDocumentEdited(edited);
		}

		this.documentEdited = edited;
	}

	isDocumentEdited(): boolean {
		if (isMacintosh) {
			return Boolean(this.win?.isDocumentEdited());
		}

		return !!this.documentEdited;
	}

	focus(options?: { force: boolean }): void {
		if (isMacintosh && options?.force) {
			electron.app.focus({ steal: true });
		}

		const win = this.win;
		if (!win) {
			return;
		}

		if (win.isMinimized()) {
			win.restore();
		}

		win.focus();
	}

	//#region Window Control Overlays

	private static readonly windowControlHeightStateStorageKey = 'windowControlHeight';

	private readonly hasWindowControlOverlay = useWindowControlsOverlay();

	updateWindowControls(options: { height?: number; backgroundColor?: string; foregroundColor?: string }): void {
		const win = this.win;
		if (!win) {
			return;
		}

		// Windows/Linux: window control overlay (WCO)
		if (this.hasWindowControlOverlay) {
			win.setTitleBarOverlay({
				color: options.backgroundColor?.trim() === '' ? undefined : options.backgroundColor,
				symbolColor: options.foregroundColor?.trim() === '' ? undefined : options.foregroundColor,
				height: options.height ? options.height - 1 : undefined // account for window border
			});
		}

		// macOS: traffic lights
		else if (isMacintosh && options.height !== undefined) {
			const verticalOffset = (options.height - 15) / 2; // 15px is the height of the traffic lights
			if (!verticalOffset) {
				win.setWindowButtonPosition(null);
			} else {
				win.setWindowButtonPosition({ x: verticalOffset, y: verticalOffset });
			}
		}
	}

	//#endregion

	//#region Fullscreen

	private transientIsNativeFullScreen: boolean | undefined = undefined;
	private joinNativeFullScreenTransition: DeferredPromise<boolean> | undefined = undefined;

	toggleFullScreen(): void {
		this.setFullScreen(!this.isFullScreen, false);
	}

	protected setFullScreen(fullscreen: boolean, fromRestore: boolean): void {
		this.setSimpleFullScreen(fullscreen);
	}

	get isFullScreen(): boolean {
		if (isMacintosh && typeof this.transientIsNativeFullScreen === 'boolean') {
			return this.transientIsNativeFullScreen;
		}

		const win = this.win;
		const isFullScreen = win?.isFullScreen();
		const isSimpleFullScreen = win?.isSimpleFullScreen();

		return Boolean(isFullScreen || isSimpleFullScreen);
	}

	private setNativeFullScreen(fullscreen: boolean, fromRestore: boolean): void {
		const win = this.win;
		if (win?.isSimpleFullScreen()) {
			win?.setSimpleFullScreen(false);
		}

		this.doSetNativeFullScreen(fullscreen, fromRestore);
	}

	private doSetNativeFullScreen(fullscreen: boolean, fromRestore: boolean): void {
		if (isMacintosh) {

			// macOS: Electron windows report `false` for `isFullScreen()` for as long
			// as the fullscreen transition animation takes place. As such, we need to
			// listen to the transition events and carry around an intermediate state
			// for knowing if we are in fullscreen or not
			// Refs: https://github.com/electron/electron/issues/35360

			this.transientIsNativeFullScreen = fullscreen;

			const joinNativeFullScreenTransition = this.joinNativeFullScreenTransition = new DeferredPromise<boolean>();
			(async () => {
				const transitioned = await Promise.race([
					joinNativeFullScreenTransition.p,
					timeout(10000).then(() => false)
				]);

				if (this.joinNativeFullScreenTransition !== joinNativeFullScreenTransition) {
					return; // another transition was requested later
				}

				this.transientIsNativeFullScreen = undefined;
				this.joinNativeFullScreenTransition = undefined;

				// There is one interesting gotcha on macOS: when you are opening a new
				// window from a fullscreen window, that new window will immediately
				// open fullscreen and emit the `enter-full-screen` event even before we
				// reach this method. In that case, we actually will timeout after 10s
				// for detecting the transition and as such it is important that we only
				// signal to leave fullscreen if the window reports as not being in fullscreen.

				if (!transitioned && fullscreen && fromRestore && this.win && !this.win.isFullScreen()) {

					// We have seen requests for fullscreen failing eventually after some
					// time, for example when an OS update was performed and windows restore.
					// In those cases a user would find a window that is not in fullscreen
					// but also does not show any custom titlebar (and thus window controls)
					// because we think the window is in fullscreen.
					//
					// As a workaround in that case we emit a warning and leave fullscreen
					// so that at least the window controls are back.

					this.logService.warn('window: native macOS fullscreen transition did not happen within 10s from restoring');

					this._onDidLeaveFullScreen.fire();
				}
			})();
		}

		const win = this.win;
		win?.setFullScreen(fullscreen);
	}

	private setSimpleFullScreen(fullscreen: boolean): void {
		const win = this.win;
		if (win?.isFullScreen()) {
			this.doSetNativeFullScreen(false, false);
		}

		win?.setSimpleFullScreen(fullscreen);
		win?.webContents.focus(); // workaround issue where focus is not going into window
	}

	//#endregion

	abstract matches(webContents: electron.WebContents): boolean;

	override dispose(): void {
		super.dispose();

		this._win = null!; // Important to dereference the window object to allow for GC
	}
}

export class ProxyWindow extends BaseWindow implements IProxyWindow {

	//#region Events

	private readonly _onWillLoad = this._register(new Emitter<ILoadEvent>());
	readonly onWillLoad = this._onWillLoad.event;

	private readonly _onDidSignalReady = this._register(new Emitter<void>());
	readonly onDidSignalReady = this._onDidSignalReady.event;

	private readonly _onDidDestroy = this._register(new Emitter<void>());
	readonly onDidDestroy = this._onDidDestroy.event;

	//#endregion


	//#region Properties

	private _id: number;
	get id(): number { return this._id; }

	protected override _win: electron.BrowserWindow;

	private _config: INativeWindowConfiguration | undefined;
	get config(): INativeWindowConfiguration | undefined { return this._config; }

	get isExtensionDevelopmentHost(): boolean { return !!(this._config?.extensionDevelopmentPath); }

	get isExtensionTestHost(): boolean { return !!(this._config?.extensionTestsPath); }

	get isExtensionDevelopmentTestFromCli(): boolean { return this.isExtensionDevelopmentHost && this.isExtensionTestHost && !this._config?.debugId; }

	//#endregion

	private readonly windowState: IWindowState;
	private currentMenuBarVisibility: MenuBarVisibility | undefined;

	private readonly whenReadyCallbacks: { (window: IProxyWindow): void }[] = [];

	private customZoomLevel: number | undefined = undefined;

	private readonly configObjectUrl = this._register(this.protocolMainService.createIPCObjectUrl<INativeWindowConfiguration>());
	private pendingLoadConfig: INativeWindowConfiguration | undefined;
	private wasLoaded = false;

	constructor(
		config: IWindowCreationOptions,
		@ILogService logService: ILogService,
		@ILoggerMainService private readonly loggerMainService: ILoggerMainService,
		@IEnvironmentMainService environmentMainService: IEnvironmentMainService,
		@ILifecycleMainService private readonly lifecycleMainService: ILifecycleMainService,
		@IProductService private readonly productService: IProductService,
		@IProtocolMainService private readonly protocolMainService: IProtocolMainService,
		@IWindowsMainService private readonly windowsMainService: IWindowsMainService,
		@IInstantiationService instantiationService: IInstantiationService
	) {
		super(environmentMainService, logService);

		//#region create browser window
		{
			// Load window state
			const [state, hasMultipleDisplays] = this.restoreWindowState(config.state);
			this.windowState = state;
			this.logService.trace('window#ctor: using window state', state);

			const options = instantiationService.invokeFunction(defaultBrowserWindowOptions, this.windowState, undefined, {
				preload: FileAccess.asFileUri('vs/base/parts/sandbox/electron-sandbox/preload.js').fsPath,
				additionalArguments: [`--vscode-window-config=${this.configObjectUrl.resource.toString()}`],
			});

			// Create the browser window
			mark('code/willCreateCodeBrowserWindow');
			this._win = new electron.BrowserWindow(options);
			mark('code/didCreateCodeBrowserWindow');

			this._id = this._win.id;
			this.setWin(this._win, options);

			// Apply some state after window creation
			this.applyState(this.windowState, hasMultipleDisplays);

			this._lastFocusTime = Date.now(); // since we show directly, we need to set the last focus time too
		}
		//#endregion

		// Eventing
		this.registerListeners();
	}

	private readyState = ReadyState.NONE;

	setReady(): void {
		this.logService.trace(`window#load: window reported ready (id: ${this._id})`);

		this.readyState = ReadyState.READY;

		// inform all waiting promises that we are ready now
		while (this.whenReadyCallbacks.length) {
			this.whenReadyCallbacks.pop()!(this);
		}

		// Events
		this._onDidSignalReady.fire();
	}

	ready(): Promise<IProxyWindow> {
		return new Promise<IProxyWindow>(resolve => {
			if (this.isReady) {
				return resolve(this);
			}

			// otherwise keep and call later when we are ready
			this.whenReadyCallbacks.push(resolve);
		});
	}

	get isReady(): boolean {
		return this.readyState === ReadyState.READY;
	}

	get whenClosedOrLoaded(): Promise<void> {
		return new Promise<void>(resolve => {

			function handle() {
				closeListener.dispose();
				loadListener.dispose();

				resolve();
			}

			const closeListener = this.onDidClose(() => handle());
			const loadListener = this.onWillLoad(() => handle());
		});
	}

	private registerListeners(): void {

		// Window error conditions to handle
		this._register(Event.fromNodeEventEmitter(this._win, 'unresponsive')(() => this.onWindowError(WindowError.UNRESPONSIVE)));
		this._register(Event.fromNodeEventEmitter(this._win.webContents, 'render-process-gone', (event, details) => details)(details => this.onWindowError(WindowError.PROCESS_GONE, { ...details })));
		this._register(Event.fromNodeEventEmitter(this._win.webContents, 'did-fail-load', (event, exitCode, reason) => ({ exitCode, reason }))(({ exitCode, reason }) => this.onWindowError(WindowError.LOAD, { reason, exitCode })));

		// Prevent windows/iframes from blocking the unload
		// through DOM events. We have our own logic for
		// unloading a window that should not be confused
		// with the DOM way.
		// (https://github.com/microsoft/vscode/issues/122736)
		this._register(Event.fromNodeEventEmitter<electron.Event>(this._win.webContents, 'will-prevent-unload')(event => event.preventDefault()));

		// Remember that we loaded
		this._register(Event.fromNodeEventEmitter(this._win.webContents, 'did-finish-load')(() => {

			// Associate properties from the load request if provided
			if (this.pendingLoadConfig) {
				this._config = this.pendingLoadConfig;

				this.pendingLoadConfig = undefined;
			}
		}));

		// Window (Un)Maximize
		this._register(this.onDidMaximize(() => {
			if (this._config) {
				this._config.maximized = true;
			}
		}));

		this._register(this.onDidUnmaximize(() => {
			if (this._config) {
				this._config.maximized = false;
			}
		}));

		// Window Fullscreen
		this._register(this.onDidEnterFullScreen(() => {
			this.sendWhenReady('vscode:enterFullScreen', CancellationToken.None);
		}));

		this._register(this.onDidLeaveFullScreen(() => {
			this.sendWhenReady('vscode:leaveFullScreen', CancellationToken.None);
		}));

		// TODO: Inject headers when requests are incoming

	}

	private async onWindowError(error: WindowError.UNRESPONSIVE): Promise<void>;
	private async onWindowError(error: WindowError.PROCESS_GONE, details: { reason: string; exitCode: number }): Promise<void>;
	private async onWindowError(error: WindowError.LOAD, details: { reason: string; exitCode: number }): Promise<void>;
	private async onWindowError(type: WindowError, details?: { reason?: string; exitCode?: number }): Promise<void> {

		switch (type) {
			case WindowError.PROCESS_GONE:
				this.logService.error(`ProxyWindow: renderer process gone (reason: ${details?.reason || '<unknown>'}, code: ${details?.exitCode || '<unknown>'})`);
				break;
			case WindowError.UNRESPONSIVE:
				this.logService.error('ProxyWindow: detected unresponsive');
				break;
			case WindowError.LOAD:
				this.logService.error(`ProxyWindow: failed to load (reason: ${details?.reason || '<unknown>'}, code: ${details?.exitCode || '<unknown>'})`);
				break;
		}

		// Inform User if non-recoverable
		switch (type) {
			case WindowError.UNRESPONSIVE:
			case WindowError.PROCESS_GONE:

				// If we run extension tests from CLI, we want to signal
				// back this state to the test runner by exiting with a
				// non-zero exit code.
				if (this.isExtensionDevelopmentTestFromCli) {
					this.lifecycleMainService.kill(1);
					return;
				}

				// If we run smoke tests, want to proceed with an orderly
				// shutdown as much as possible by destroying the window
				// and then calling the normal `quit` routine.
				if (this.environmentMainService.args['enable-smoke-test-driver']) {
					await this.destroyWindow(false, false);
					this.lifecycleMainService.quit(); // still allow for an orderly shutdown
					return;
				}

				// Unresponsive
				if (type === WindowError.UNRESPONSIVE) {
					if (this.isExtensionDevelopmentHost || this.isExtensionTestHost || (this._win && this._win.webContents && this._win.webContents.isDevToolsOpened())) {
						// TODO@electron Workaround for https://github.com/microsoft/vscode/issues/56994
						// In certain cases the window can report unresponsiveness because a breakpoint was hit
						// and the process is stopped executing. The most typical cases are:
						// - devtools are opened and debugging happens
						// - window is an extensions development host that is being debugged
						// - window is an extension test development host that is being debugged
						return;
					}

					// TODO: Show Dialog
					this.logService.error('ProxyWindow unresponsive');
				}

				// Process gone
				else if (type === WindowError.PROCESS_GONE) {
					let message: string;
					if (!details) {
						message = localize('appGone', "The window terminated unexpectedly");
					} else {
						message = localize('appGoneDetails', "The window terminated unexpectedly (reason: '{0}', code: '{1}')", details.reason, details.exitCode ?? '<unknown>');
					}

					// TODO:  Show Dialog
				}
				break;
		}
	}

	private async destroyWindow(reopen: boolean, skipRestoreEditors: boolean): Promise<void> {


		// 'close' event will not be fired on destroy(), so signal crash via explicit event
		this._onDidDestroy.fire();

		try {
			// ask the windows service to open a new fresh window if specified
			if (reopen && this._config) {


				// Delegate to windows service
				const window = (await this.windowsMainService.open({
					context: OpenContext.API,
					userEnv: this._config.userEnv,
					cli: {
						...this.environmentMainService.args,
						_: [] // we pass in the workspace to open explicitly via `urisToOpen`
					},
					forceNewWindow: true,
				}));
				window?.focus();
			}
		} finally {
			// make sure to destroy the window as its renderer process is gone. do this
			// after the code for reopening the window, to prevent the entire application
			// from quitting when the last window closes as a result.
			this._win?.destroy();
		}
	}


	addTabbedWindow(window: IProxyWindow): void {
		if (isMacintosh && window.win) {
			this._win.addTabbedWindow(window.win);
		}
	}

	load(configuration: INativeWindowConfiguration, options: ILoadOptions = Object.create(null)): void {
		this.logService.trace(`window#load: attempt to load window (id: ${this._id})`);

		// Clear Title and Filename if needed
		if (!options.isReload) {
			if (this.getRepresentedFilename()) {
				this.setRepresentedFilename('');
			}

			this._win.setTitle(this.productService.nameLong);
		}

		// Update configuration values based on our window context
		// and set it into the config object URL for usage.
		this.updateConfiguration(configuration, options);

		// If this is the first time the window is loaded, we associate the paths
		// directly with the window because we assume the loading will just work
		if (this.readyState === ReadyState.NONE) {
			this._config = configuration;
		}

		// Otherwise, the window is currently showing a folder and if there is an
		// unload handler preventing the load, we cannot just associate the paths
		// because the loading might be vetoed. Instead we associate it later when
		// the window load event has fired.
		else {
			this.pendingLoadConfig = configuration;
		}

		// Indicate we are navigting now
		this.readyState = ReadyState.NAVIGATING;

		// Load URL
		this._win.loadURL(FileAccess.asBrowserUri(`vs/croxy/electron-sandbox/workbench/workbench${this.environmentMainService.isBuilt ? '' : '-dev'}.html`).toString(true));

		// Remember that we did load
		const wasLoaded = this.wasLoaded;
		this.wasLoaded = true;

		// Make window visible if it did not open in N seconds because this indicates an error
		// Only do this when running out of sources and not when running tests
		if (!this.environmentMainService.isBuilt && !this.environmentMainService.extensionTestsLocationURI) {
			this._register(new RunOnceScheduler(() => {
				if (this._win && !this._win.isVisible() && !this._win.isMinimized()) {
					this._win.show();
					this.focus({ force: true });
					this._win.webContents.openDevTools();
				}
			}, 10000)).schedule();
		}

		// Event
		this._onWillLoad.fire({ reason: options.isReload ? LoadReason.RELOAD : wasLoaded ? LoadReason.LOAD : LoadReason.INITIAL });
	}

	private updateConfiguration(configuration: INativeWindowConfiguration, options: ILoadOptions): void {

		// If this window was loaded before from the command line
		// (as indicated by VSCODE_CLI environment), make sure to
		// preserve that user environment in subsequent loads,
		// unless the new configuration context was also a CLI
		// (for https://github.com/microsoft/vscode/issues/108571)
		// Also, preserve the environment if we're loading from an
		// extension development host that had its environment set
		// (for https://github.com/microsoft/vscode/issues/123508)
		const currentUserEnv = (this._config ?? this.pendingLoadConfig)?.userEnv;
		if (currentUserEnv) {
			const shouldPreserveLaunchCliEnvironment = isLaunchedFromCli(currentUserEnv) && !isLaunchedFromCli(configuration.userEnv);
			const shouldPreserveDebugEnvironmnet = this.isExtensionDevelopmentHost;
			if (shouldPreserveLaunchCliEnvironment || shouldPreserveDebugEnvironmnet) {
				configuration.userEnv = { ...currentUserEnv, ...configuration.userEnv }; // still allow to override certain environment as passed in
			}
		}

		// If named pipe was instantiated for the crashpad_handler process, reuse the same
		// pipe for new app instances connecting to the original app instance.
		// Ref: https://github.com/microsoft/vscode/issues/115874
		if (process.env['CHROME_CRASHPAD_PIPE_NAME']) {
			Object.assign(configuration.userEnv, {
				CHROME_CRASHPAD_PIPE_NAME: process.env['CHROME_CRASHPAD_PIPE_NAME']
			});
		}

		// Add disable-extensions to the config, but do not preserve it on currentConfig or
		// pendingLoadConfig so that it is applied only on this load
		if (options.disableExtensions !== undefined) {
			configuration['disable-extensions'] = options.disableExtensions;
		}

		// Update window related properties
		try {
			configuration.handle = VSBuffer.wrap(this._win.getNativeWindowHandle());
		} catch (error) {
			this.logService.error(`Error getting native window handle: ${error}`);
		}
		configuration.fullscreen = this.isFullScreen;
		configuration.maximized = this._win.isMaximized();
		configuration.zoomLevel = this.getZoomLevel();
		configuration.isCustomZoomLevel = typeof this.customZoomLevel === 'number';

		// Update with latest perf marks
		mark('code/willOpenNewWindow');
		configuration.perfMarks = getMarks();

		// Update in config object URL for usage in renderer
		this.configObjectUrl.update(configuration);
	}

	async reload(cli?: NativeParsedArgs): Promise<void> {

		// Copy our current config for reuse
		const configuration = Object.assign({}, this._config);


		// Some configuration things get inherited if the window is being reloaded and we are
		// in extension development mode. These options are all development related.
		if (this.isExtensionDevelopmentHost && cli) {
			configuration.verbose = cli.verbose;
			configuration.debugId = cli.debugId;
			configuration.extensionEnvironment = cli.extensionEnvironment;
			configuration['inspect-extensions'] = cli['inspect-extensions'];
			configuration['inspect-brk-extensions'] = cli['inspect-brk-extensions'];
			configuration['extensions-dir'] = cli['extensions-dir'];
		}

		configuration.accessibilitySupport = electron.app.isAccessibilitySupportEnabled();
		configuration.isInitialStartup = false; // since this is a reload

		configuration.logLevel = this.loggerMainService.getLogLevel();
		configuration.loggers = {
			window: this.loggerMainService.getRegisteredLoggers(this.id),
			global: this.loggerMainService.getRegisteredLoggers()
		};

		// Load config
		this.load(configuration, { isReload: true, disableExtensions: cli?.['disable-extensions'] });
	}

	serializeWindowState(): IWindowState {
		if (!this._win) {
			return defaultWindowState();
		}

		// fullscreen gets special treatment
		if (this.isFullScreen) {
			let display: electron.Display | undefined;
			try {
				display = electron.screen.getDisplayMatching(this.getBounds());
			} catch (error) {
				// Electron has weird conditions under which it throws errors
				// e.g. https://github.com/microsoft/vscode/issues/100334 when
				// large numbers are passed in
			}

			const defaultState = defaultWindowState();

			return {
				mode: WindowMode.Fullscreen,
				display: display ? display.id : undefined,

				// Still carry over window dimensions from previous sessions
				// if we can compute it in fullscreen state.
				// does not seem possible in all cases on Linux for example
				// (https://github.com/microsoft/vscode/issues/58218) so we
				// fallback to the defaults in that case.
				width: this.windowState.width || defaultState.width,
				height: this.windowState.height || defaultState.height,
				x: this.windowState.x || 0,
				y: this.windowState.y || 0,
				zoomLevel: this.customZoomLevel
			};
		}

		const state: IWindowState = Object.create(null);
		let mode: WindowMode;

		// get window mode
		if (!isMacintosh && this._win.isMaximized()) {
			mode = WindowMode.Maximized;
		} else {
			mode = WindowMode.Normal;
		}

		// we don't want to save minimized state, only maximized or normal
		if (mode === WindowMode.Maximized) {
			state.mode = WindowMode.Maximized;
		} else {
			state.mode = WindowMode.Normal;
		}

		// only consider non-minimized window states
		if (mode === WindowMode.Normal || mode === WindowMode.Maximized) {
			let bounds: electron.Rectangle;
			if (mode === WindowMode.Normal) {
				bounds = this.getBounds();
			} else {
				bounds = this._win.getNormalBounds(); // make sure to persist the normal bounds when maximized to be able to restore them
			}

			state.x = bounds.x;
			state.y = bounds.y;
			state.width = bounds.width;
			state.height = bounds.height;
		}

		state.zoomLevel = this.customZoomLevel;

		return state;
	}

	private restoreWindowState(state?: IWindowState): [IWindowState, boolean? /* has multiple displays */] {
		mark('code/willRestoreProxyWindowState');

		let hasMultipleDisplays = false;
		if (state) {

			// Window zoom
			this.customZoomLevel = state.zoomLevel;

			// Window dimensions
			try {
				const displays = electron.screen.getAllDisplays();
				hasMultipleDisplays = displays.length > 1;

				state = WindowStateValidator.validateWindowState(this.logService, state, displays);
			} catch (err) {
				this.logService.warn(`Unexpected error validating window state: ${err}\n${err.stack}`); // somehow display API can be picky about the state to validate
			}
		}

		mark('code/didRestoreProxyWindowState');

		return [state || defaultWindowState(), hasMultipleDisplays];
	}

	getBounds(): electron.Rectangle {
		const [x, y] = this._win.getPosition();
		const [width, height] = this._win.getSize();

		return { x, y, width, height };
	}

	protected override setFullScreen(fullscreen: boolean, fromRestore: boolean): void {
		super.setFullScreen(fullscreen, fromRestore);

		// Events
		this.sendWhenReady(fullscreen ? 'vscode:enterFullScreen' : 'vscode:leaveFullScreen', CancellationToken.None);

		// Respect configured menu bar visibility or default to toggle if not set
		if (this.currentMenuBarVisibility) {
			this.setMenuBarVisibility(this.currentMenuBarVisibility, false);
		}
	}

	private setMenuBarVisibility(visibility: MenuBarVisibility, notify: boolean = true): void {
		if (isMacintosh) {
			return; // ignore for macOS platform
		}

		if (visibility === 'toggle') {
			if (notify) {
				this.send('vscode:showInfoMessage', localize('hiddenMenuBar', "You can still access the menu bar by pressing the Alt-key."));
			}
		}

		if (visibility === 'hidden') {
			// for some weird reason that I have no explanation for, the menu bar is not hiding when calling
			// this without timeout (see https://github.com/microsoft/vscode/issues/19777). there seems to be
			// a timing issue with us opening the first window and the menu bar getting created. somehow the
			// fact that we want to hide the menu without being able to bring it back via Alt key makes Electron
			// still show the menu. Unable to reproduce from a simple Hello World application though...
			setTimeout(() => {
				this.doSetMenuBarVisibility(visibility);
			});
		} else {
			this.doSetMenuBarVisibility(visibility);
		}
	}

	private doSetMenuBarVisibility(visibility: MenuBarVisibility): void {
		const isFullscreen = this.isFullScreen;

		switch (visibility) {
			case ('classic'):
				this._win.setMenuBarVisibility(!isFullscreen);
				this._win.autoHideMenuBar = isFullscreen;
				break;

			case ('visible'):
				this._win.setMenuBarVisibility(true);
				this._win.autoHideMenuBar = false;
				break;

			case ('toggle'):
				this._win.setMenuBarVisibility(false);
				this._win.autoHideMenuBar = true;
				break;

			case ('hidden'):
				this._win.setMenuBarVisibility(false);
				this._win.autoHideMenuBar = false;
				break;
		}
	}

	notifyZoomLevel(zoomLevel: number | undefined): void {
		this.customZoomLevel = zoomLevel;
	}

	private getZoomLevel(): number | undefined {
		if (typeof this.customZoomLevel === 'number') {
			return this.customZoomLevel;
		}

		// TODO:
		return 1;
	}

	close(): void {
		this._win?.close();
	}

	sendWhenReady(channel: string, token: CancellationToken, ...args: any[]): void {
		if (this.isReady) {
			this.send(channel, ...args);
		} else {
			this.ready().then(() => {
				if (!token.isCancellationRequested) {
					this.send(channel, ...args);
				}
			});
		}
	}

	send(channel: string, ...args: any[]): void {
		if (this._win) {
			if (this._win.isDestroyed() || this._win.webContents.isDestroyed()) {
				this.logService.warn(`Sending IPC message to channel '${channel}' for window that is destroyed`);
				return;
			}

			try {
				this._win.webContents.send(channel, ...args);
			} catch (error) {
				this.logService.warn(`Error sending IPC message to channel '${channel}' of window ${this._id}: ${toErrorMessage(error)}`);
			}
		}
	}

	matches(webContents: electron.WebContents): boolean {
		return this._win?.webContents.id === webContents.id;
	}

	override dispose(): void {
		super.dispose();

		// Deregister the loggers for this window
		this.loggerMainService.deregisterLoggers(this.id);
	}
}
