/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { app, BrowserWindow, WebContents } from 'electron';
import { arch, hostname, release } from 'os';
import { CancellationToken } from '../../../base/common/cancellation.js';
import { Emitter, Event } from '../../../base/common/event.js';
import { Disposable, DisposableStore, IDisposable } from '../../../base/common/lifecycle.js';
import { Schemas } from '../../../base/common/network.js';
import { getMarks, mark } from '../../../base/common/performance.js';
import { IProcessEnvironment } from '../../../base/common/platform.js';
import { assertIsDefined } from '../../../base/common/types.js';
import { getNLSLanguage, getNLSMessages } from '../../../nls.js';
import { NativeParsedArgs } from '../../environment/common/argv.js';
import { IEnvironmentMainService } from '../../environment/electron-main/environmentMainService.js';
import { IInstantiationService } from '../../instantiation/common/instantiation.js';
import { ILifecycleMainService } from '../../lifecycle/electron-main/lifecycleMainService.js';
import { ILogService } from '../../log/common/log.js';
import { ILoggerMainService } from '../../log/electron-main/loggerService.js';
import product from '../../product/common/product.js';
import { IProtocolMainService } from '../../protocol/electron-main/protocol.js';
import { INativeWindowConfiguration, IOpenEmptyWindowOptions, titlebarStyleDefaultOverride } from '../../window/common/window.js';
import { IProxyWindow, UnloadReason } from '../../window/electron-main/window.js';
import { ProxyWindow } from './windowImpl.js';
import { getLastFocused, IOpenConfiguration, IOpenEmptyConfiguration, IWindowsCountChangedEvent, IWindowsMainService } from './windows.js';
import { ICSSDevelopmentService } from '../../cssDev/node/cssDevService.js';

//#region Helper Interfaces

interface IOpenBrowserWindowOptions {
	readonly userEnv?: IProcessEnvironment;
	readonly cli?: NativeParsedArgs;


	readonly remoteAuthority?: string;

	readonly initialStartup?: boolean;

	readonly forceNewWindow?: boolean;
	readonly forceNewTabbedWindow?: boolean;
	readonly windowToUse?: IProxyWindow;

	readonly forceProfile?: string;
	readonly forceTempProfile?: boolean;
}


//#endregion

export class WindowsMainService extends Disposable implements IWindowsMainService {

	declare readonly _serviceBrand: undefined;

	private readonly _onDidOpenWindow = this._register(new Emitter<IProxyWindow>());
	readonly onDidOpenWindow = this._onDidOpenWindow.event;

	private readonly _onDidSignalReadyWindow = this._register(new Emitter<IProxyWindow>());
	readonly onDidSignalReadyWindow = this._onDidSignalReadyWindow.event;

	private readonly _onDidDestroyWindow = this._register(new Emitter<IProxyWindow>());
	readonly onDidDestroyWindow = this._onDidDestroyWindow.event;

	private readonly _onDidChangeWindowsCount = this._register(new Emitter<IWindowsCountChangedEvent>());
	readonly onDidChangeWindowsCount = this._onDidChangeWindowsCount.event;

	private readonly _onDidMaximizeWindow = this._register(new Emitter<IProxyWindow>());
	readonly onDidMaximizeWindow = this._onDidMaximizeWindow.event;

	private readonly _onDidUnmaximizeWindow = this._register(new Emitter<IProxyWindow>());
	readonly onDidUnmaximizeWindow = this._onDidUnmaximizeWindow.event;

	private readonly _onDidChangeFullScreen = this._register(new Emitter<{ window: IProxyWindow; fullscreen: boolean }>());
	readonly onDidChangeFullScreen = this._onDidChangeFullScreen.event;

	private readonly _onDidTriggerSystemContextMenu = this._register(new Emitter<{ window: IProxyWindow; x: number; y: number }>());
	readonly onDidTriggerSystemContextMenu = this._onDidTriggerSystemContextMenu.event;

	private readonly windows = new Map<number, IProxyWindow>();

	// TODO: window state handler

	constructor(
		private readonly initialUserEnv: IProcessEnvironment,
		@ILogService private readonly logService: ILogService,
		@ILoggerMainService private readonly loggerService: ILoggerMainService,
		@IEnvironmentMainService private readonly environmentMainService: IEnvironmentMainService,
		@ILifecycleMainService private readonly lifecycleMainService: ILifecycleMainService,
		@IInstantiationService private readonly instantiationService: IInstantiationService,
		@IProtocolMainService private readonly protocolMainService: IProtocolMainService,
		@ICSSDevelopmentService private readonly cssDevelopmentService: ICSSDevelopmentService

	) {
		super();

		this.registerListeners();
	}

	private registerListeners(): void {

		// Update valid roots in protocol service for extension dev windows
		this._register(this.onDidSignalReadyWindow(window => {
			if (window.config?.extensionDevelopmentPath || window.config?.extensionTestsPath) {
				const disposables = new DisposableStore();
				disposables.add(Event.any(window.onDidClose, window.onDidDestroy)(() => disposables.dispose()));

				// Allow access to extension development path
				if (window.config.extensionDevelopmentPath) {
					for (const extensionDevelopmentPath of window.config.extensionDevelopmentPath) {
						disposables.add(this.protocolMainService.addValidFileRoot(extensionDevelopmentPath));
					}
				}

				// Allow access to extension tests path
				if (window.config.extensionTestsPath) {
					disposables.add(this.protocolMainService.addValidFileRoot(window.config.extensionTestsPath));
				}
			}
		}));
	}

	openEmptyWindow(openConfig: IOpenEmptyConfiguration, options?: IOpenEmptyWindowOptions): Promise<IProxyWindow> {
		const cli = this.environmentMainService.args;
		const remoteAuthority = options?.remoteAuthority || undefined;
		const forceEmpty = true;
		const forceReuseWindow = options?.forceReuseWindow;
		const forceNewWindow = !forceReuseWindow;

		return this.open({ ...openConfig, cli, forceEmpty, forceNewWindow, forceReuseWindow, remoteAuthority, forceTempProfile: options?.forceTempProfile, forceProfile: options?.forceProfile });
	}

	openExistingWindow(window: IProxyWindow, openConfig: IOpenConfiguration): void {

		// Bring window to front
		window.focus();

	}

	async open(openConfig: IOpenConfiguration): Promise<IProxyWindow> {
		this.logService.trace('windowsManager#open');

		return this.openInBrowserWindow({
			userEnv: openConfig.userEnv,
			cli: openConfig.cli,
			initialStartup: openConfig.initialStartup,
			forceNewWindow: true,
			forceNewTabbedWindow: openConfig.forceNewTabbedWindow,
			forceProfile: openConfig.forceProfile,
			forceTempProfile: openConfig.forceTempProfile
		});
	}

	private async openInBrowserWindow(options: IOpenBrowserWindowOptions): Promise<IProxyWindow> {
		const lastActiveWindow = this.getLastActiveWindow();

		let window: IProxyWindow | undefined;
		if (!options.forceNewWindow && !options.forceNewTabbedWindow) {
			window = options.windowToUse || lastActiveWindow;
			if (window) {
				window.focus();
			}
		}

		// Build up the window configuration from provided options, config and environment
		const configuration: INativeWindowConfiguration = {

			// Inherit CLI arguments from environment and/or
			// the specific properties from this launch if provided
			...this.environmentMainService.args,
			...options.cli,

			windowId: -1,	// Will be filled in by the window once loaded later

			mainPid: process.pid,

			appRoot: this.environmentMainService.appRoot,
			execPath: process.execPath,


			homeDir: this.environmentMainService.userHome.with({ scheme: Schemas.file }).fsPath,
			tmpDir: this.environmentMainService.tmpDir.with({ scheme: Schemas.file }).fsPath,
			userDataDir: this.environmentMainService.userDataPath,

			userEnv: { ...this.initialUserEnv, ...options.userEnv },

			nls: {
				messages: getNLSMessages(),
				language: getNLSLanguage()
			},

			logLevel: this.loggerService.getLogLevel(),
			loggers: {
				window: [],
				global: this.loggerService.getRegisteredLoggers()
			},
			logsPath: this.environmentMainService.logsHome.with({ scheme: Schemas.file }).fsPath,

			product,
			isInitialStartup: options.initialStartup,
			perfMarks: getMarks(),
			os: { release: release(), hostname: hostname(), arch: arch() },

			overrideDefaultTitlebarStyle: titlebarStyleDefaultOverride,
			accessibilitySupport: app.accessibilitySupportEnabled,
			// TODO: themeService
			colorScheme: { dark: false, highContrast: false },

			cssModules: this.cssDevelopmentService.isEnabled ? await this.cssDevelopmentService.getCssModules() : undefined
		};

		// New window
		if (!window) {
			// TODO: window state
			// const state = this.windowsStateHandler.getNewWindowState(configuration);

			// Create the window
			mark('code/willCreateProxyWindow');
			const createdWindow = window = this.instantiationService.createInstance(ProxyWindow, {
				state: {},
				extensionDevelopmentPath: configuration.extensionDevelopmentPath,
				isExtensionTestHost: !!configuration.extensionTestsPath
			});
			mark('code/didCreateProxyWindow');

			// Add to our list of windows
			this.windows.set(createdWindow.id, createdWindow);

			// Indicate new window via event
			this._onDidOpenWindow.fire(createdWindow);

			// Indicate number change via event
			this._onDidChangeWindowsCount.fire({ oldCount: this.getWindowCount() - 1, newCount: this.getWindowCount() });

			// Window Events
			const disposables = new DisposableStore();
			disposables.add(createdWindow.onDidSignalReady(() => this._onDidSignalReadyWindow.fire(createdWindow)));
			disposables.add(Event.once(createdWindow.onDidClose)(() => this.onWindowClosed(createdWindow, disposables)));
			disposables.add(Event.once(createdWindow.onDidDestroy)(() => this.onWindowDestroyed(createdWindow)));
			disposables.add(createdWindow.onDidMaximize(() => this._onDidMaximizeWindow.fire(createdWindow)));
			disposables.add(createdWindow.onDidUnmaximize(() => this._onDidUnmaximizeWindow.fire(createdWindow)));
			disposables.add(createdWindow.onDidEnterFullScreen(() => this._onDidChangeFullScreen.fire({ window: createdWindow, fullscreen: true })));
			disposables.add(createdWindow.onDidLeaveFullScreen(() => this._onDidChangeFullScreen.fire({ window: createdWindow, fullscreen: false })));
			disposables.add(createdWindow.onDidTriggerSystemContextMenu(({ x, y }) => this._onDidTriggerSystemContextMenu.fire({ window: createdWindow, x, y })));

			const webContents = assertIsDefined(createdWindow.win?.webContents);
			webContents.removeAllListeners('devtools-reload-page'); // remove built in listener so we can handle this on our own
			disposables.add(Event.fromNodeEventEmitter(webContents, 'devtools-reload-page')(() => this.lifecycleMainService.reload(createdWindow)));

			// Lifecycle
			this.lifecycleMainService.registerWindow(createdWindow);
		}

		// Existing window
		else {

			// Some configuration things get inherited if the window is being reused and we are
			// in extension development host mode. These options are all development related.
			const currentWindowConfig = window.config;
			if (!configuration.extensionDevelopmentPath && currentWindowConfig?.extensionDevelopmentPath) {
				configuration.extensionDevelopmentPath = currentWindowConfig.extensionDevelopmentPath;
				configuration.extensionDevelopmentKind = currentWindowConfig.extensionDevelopmentKind;
				configuration['enable-proposed-api'] = currentWindowConfig['enable-proposed-api'];
				configuration.verbose = currentWindowConfig.verbose;
				configuration['inspect-extensions'] = currentWindowConfig['inspect-extensions'];
				configuration['inspect-brk-extensions'] = currentWindowConfig['inspect-brk-extensions'];
				configuration.debugId = currentWindowConfig.debugId;
				configuration.extensionEnvironment = currentWindowConfig.extensionEnvironment;
				configuration['extensions-dir'] = currentWindowConfig['extensions-dir'];
				configuration['disable-extensions'] = currentWindowConfig['disable-extensions'];
			}
			configuration.loggers = {
				global: configuration.loggers.global,
				window: currentWindowConfig?.loggers.window ?? configuration.loggers.window
			};
		}

		// Update window identifier and session now
		// that we have the window object in hand.
		configuration.windowId = window.id;

		// If the window was already loaded, make sure to unload it
		// first and only load the new configuration if that was
		// not vetoed
		if (window.isReady) {
			this.lifecycleMainService.unload(window, UnloadReason.LOAD).then(async veto => {
				if (!veto) {
					await this.doOpenInBrowserWindow(window, configuration, options);
				}
			});
		} else {
			await this.doOpenInBrowserWindow(window, configuration, options);
		}

		return window;
	}

	private async doOpenInBrowserWindow(window: IProxyWindow, configuration: INativeWindowConfiguration, options: IOpenBrowserWindowOptions): Promise<void> {
		// Load it
		window.load(configuration);
	}

	private onWindowClosed(window: IProxyWindow, disposables: IDisposable): void {

		// Remove from our list so that Electron can clean it up
		this.windows.delete(window.id);

		// Emit
		this._onDidChangeWindowsCount.fire({ oldCount: this.getWindowCount() + 1, newCount: this.getWindowCount() });

		// Clean up
		disposables.dispose();
	}

	private onWindowDestroyed(window: IProxyWindow): void {

		// Remove from our list so that Electron can clean it up
		this.windows.delete(window.id);

		// Emit
		this._onDidDestroyWindow.fire(window);
	}

	getFocusedWindow(): IProxyWindow | undefined {
		const window = BrowserWindow.getFocusedWindow();
		if (window) {
			return this.getWindowById(window.id);
		}

		return undefined;
	}

	getLastActiveWindow(): IProxyWindow | undefined {
		return this.doGetLastActiveWindow(this.getWindows());
	}

	private doGetLastActiveWindow(windows: IProxyWindow[]): IProxyWindow | undefined {
		return getLastFocused(windows);
	}

	sendToFocused(channel: string, ...args: any[]): void {
		const focusedWindow = this.getFocusedWindow() || this.getLastActiveWindow();

		focusedWindow?.sendWhenReady(channel, CancellationToken.None, ...args);
	}

	sendToOpeningWindow(channel: string, ...args: any[]): void {
		this._register(Event.once(this.onDidSignalReadyWindow)(window => {
			window.sendWhenReady(channel, CancellationToken.None, ...args);
		}));
	}

	sendToAll(channel: string, payload?: any, windowIdsToIgnore?: number[]): void {
		for (const window of this.getWindows()) {
			if (windowIdsToIgnore && windowIdsToIgnore.indexOf(window.id) >= 0) {
				continue; // do not send if we are instructed to ignore it
			}

			window.sendWhenReady(channel, CancellationToken.None, payload);
		}
	}

	getWindows(): IProxyWindow[] {
		return Array.from(this.windows.values());
	}

	getWindowCount(): number {
		return this.windows.size;
	}

	getWindowById(windowId: number): IProxyWindow | undefined {
		return this.windows.get(windowId);
	}

	getWindowByWebContents(webContents: WebContents): IProxyWindow | undefined {
		const browserWindow = BrowserWindow.fromWebContents(webContents);
		if (!browserWindow) {
			return undefined;
		}

		const window = this.getWindowById(browserWindow.id);

		return window?.matches(webContents) ? window : undefined;
	}
}
