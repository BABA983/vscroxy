import { Disposable } from '../../../base/common/lifecycle.js';
import { AddFirstParameterToFunctions } from '../../../base/common/types.js';
import { createDecorator } from '../../instantiation/common/instantiation.js';
import { ILifecycleMainService, IRelaunchOptions } from '../../lifecycle/electron-main/lifecycleMainService.js';
import { IProxyWindow } from '../../window/electron-main/window.js';
import { IWindowsMainService } from '../../windows/electron-main/windows.js';
import { ICommonNativeHostService, INativeHostOptions } from '../common/native.js';

export interface INativeHostMainService extends AddFirstParameterToFunctions<ICommonNativeHostService, Promise<unknown> /* only methods, not events */, number | undefined /* window ID */> { }

export const INativeHostMainService = createDecorator<INativeHostMainService>('nativeHostMainService');

export class NativeHostMainService extends Disposable implements INativeHostMainService {
	declare readonly _serviceBrand: undefined;

	constructor(
		@IWindowsMainService private readonly windowsMainService: IWindowsMainService,
		@ILifecycleMainService private readonly lifecycleMainService: ILifecycleMainService) {
		super();
	}

	//#region Properties

	get windowId(): never {
		throw new Error('Not implemented in electron-main');
	}

	//#endregion

	async notifyReady(windowId: number | undefined): Promise<void> {
		const window = this.codeWindowById(windowId);
		window?.setReady();
	}

	async relaunch(windowId: number | undefined, options?: IRelaunchOptions): Promise<void> {
		return this.lifecycleMainService.relaunch(options);
	}

	async reload(windowId: number | undefined, options?: { disableExtensions?: boolean }): Promise<void> {
		const window = this.codeWindowById(windowId);
		if (window) {
			// Proceed normally to reload the window
			return this.lifecycleMainService.reload(window, options?.disableExtensions !== undefined ? { _: [], 'disable-extensions': options.disableExtensions } : undefined);
		}
	}

	async closeWindow(windowId: number | undefined, options?: INativeHostOptions): Promise<void> {
		const window = this.windowById(options?.targetWindowId, windowId);
		return window?.win?.close();
	}

	async quit(_windowId: number | undefined): Promise<void> {
		this.lifecycleMainService.quit();
	}

	async exit(windowId: number | undefined, code: number): Promise<void> {
		await this.lifecycleMainService.kill(code);
	}

	private windowById(windowId: number | undefined, fallbackCodeWindowId?: number): IProxyWindow | undefined {
		return this.codeWindowById(windowId) ?? this.codeWindowById(fallbackCodeWindowId);
	}

	private codeWindowById(windowId: number | undefined): IProxyWindow | undefined {
		if (typeof windowId !== 'number') {
			return undefined;
		}

		return this.windowsMainService.getWindowById(windowId);
	}
}
