import { setFullscreen } from '../../base/browser/browser.js';
import { addDisposableListener, EventType } from '../../base/browser/dom.js';
import { mainWindow } from '../../base/browser/window.js';
import { RunOnceScheduler } from '../../base/common/async.js';
import { toErrorMessage } from '../../base/common/errorMessage.js';
import { Event } from '../../base/common/event.js';
import { isMacintosh } from '../../base/common/platform.js';
import { ipcRenderer } from '../../base/parts/sandbox/electron-sandbox/globals.js';
import { localize } from '../../nls.js';
import { IConfigurationService } from '../../platform/configuration/common/configuration.js';
import { INativeHostService } from '../../platform/native/common/native.js';
import { BaseWindow } from '../browser/window.js';
import { INativeWorkbenchEnvironmentService } from '../services/environment/electron-sandbox/environmentService.js';
import { IHostService } from '../services/host/browser/host.js';
import { IWorkbenchLayoutService } from '../services/layout/browser/layoutService.js';
import { BeforeShutdownErrorEvent, BeforeShutdownEvent, ILifecycleService, ShutdownReason, WillShutdownEvent } from '../services/lifecycle/common/lifecycle.js';

export class NativeWindow extends BaseWindow {

	constructor(
		@IWorkbenchLayoutService private readonly layoutService: IWorkbenchLayoutService,
		@IHostService hostService: IHostService,
		@INativeWorkbenchEnvironmentService private readonly nativeEnvironmentService: INativeWorkbenchEnvironmentService,
		@ILifecycleService private readonly lifecycleService: ILifecycleService,
		@IConfigurationService private readonly configurationService: IConfigurationService,
		@INativeHostService private readonly nativeHostService: INativeHostService,
	) {
		super(mainWindow, undefined, hostService, nativeEnvironmentService);

		this.registerListeners();
	}

	protected registerListeners(): void {
		// Layout
		this._register(addDisposableListener(mainWindow, EventType.RESIZE, () => this.layoutService.layout()));

		// Fullscreen Events
		ipcRenderer.on('vscode:enterFullScreen', () => setFullscreen(true, mainWindow));
		ipcRenderer.on('vscode:leaveFullScreen', () => setFullscreen(false, mainWindow));

		// Lifecycle
		// this._register(this.lifecycleService.onBeforeShutdown(e => this.onBeforeShutdown(e)));
		// this._register(this.lifecycleService.onBeforeShutdownError(e => this.onBeforeShutdownError(e)));
		// this._register(this.lifecycleService.onWillShutdown(e => this.onWillShutdown(e)));
	}

	//#region Window Lifecycle

	// private onBeforeShutdown({ veto, reason }: BeforeShutdownEvent): void {
	// 	if (reason === ShutdownReason.CLOSE) {
	// 		const confirmBeforeCloseSetting = this.configurationService.getValue<'always' | 'never' | 'keyboardOnly'>('window.confirmBeforeClose');

	// 		const confirmBeforeClose = confirmBeforeCloseSetting === 'always' || (confirmBeforeCloseSetting === 'keyboardOnly' && ModifierKeyEmitter.getInstance().isModifierPressed);
	// 		if (confirmBeforeClose) {

	// 			// When we need to confirm on close or quit, veto the shutdown
	// 			// with a long running promise to figure out whether shutdown
	// 			// can proceed or not.

	// 			return veto((async () => {
	// 				let actualReason: ShutdownReason = reason;
	// 				if (reason === ShutdownReason.CLOSE && !isMacintosh) {
	// 					const windowCount = await this.nativeHostService.getWindowCount();
	// 					if (windowCount === 1) {
	// 						actualReason = ShutdownReason.QUIT; // Windows/Linux: closing last window means to QUIT
	// 					}
	// 				}

	// 				let confirmed = true;
	// 				if (confirmBeforeClose) {
	// 					confirmed = await this.instantiationService.invokeFunction(accessor => NativeWindow.confirmOnShutdown(accessor, actualReason));
	// 				}

	// 				// Progress for long running shutdown
	// 				if (confirmed) {
	// 					this.progressOnBeforeShutdown(reason);
	// 				}

	// 				return !confirmed;
	// 			})(), 'veto.confirmBeforeClose');
	// 		}
	// 	}

	// 	// Progress for long running shutdown
	// 	this.progressOnBeforeShutdown(reason);
	// }

	// private progressOnBeforeShutdown(reason: ShutdownReason): void {
	// 	this.progressService.withProgress({
	// 		location: ProgressLocation.Window, 	// use window progress to not be too annoying about this operation
	// 		delay: 800,							// delay so that it only appears when operation takes a long time
	// 		title: this.toShutdownLabel(reason, false),
	// 	}, () => {
	// 		return Event.toPromise(Event.any(
	// 			this.lifecycleService.onWillShutdown, 	// dismiss this dialog when we shutdown
	// 			this.lifecycleService.onShutdownVeto, 	// or when shutdown was vetoed
	// 			this.dialogService.onWillShowDialog		// or when a dialog asks for input
	// 		));
	// 	});
	// }

	// private onBeforeShutdownError({ error, reason }: BeforeShutdownErrorEvent): void {
	// 	this.dialogService.error(this.toShutdownLabel(reason, true), localize('shutdownErrorDetail', "Error: {0}", toErrorMessage(error)));
	// }

	// private onWillShutdown({ reason, force, joiners }: WillShutdownEvent): void {

	// 	// Delay so that the dialog only appears after timeout
	// 	const shutdownDialogScheduler = new RunOnceScheduler(() => {
	// 		const pendingJoiners = joiners();

	// 		this.progressService.withProgress({
	// 			location: ProgressLocation.Dialog, 				// use a dialog to prevent the user from making any more interactions now
	// 			buttons: [this.toForceShutdownLabel(reason)],	// allow to force shutdown anyway
	// 			cancellable: false,								// do not allow to cancel
	// 			sticky: true,									// do not allow to dismiss
	// 			title: this.toShutdownLabel(reason, false),
	// 			detail: pendingJoiners.length > 0 ? localize('willShutdownDetail', "The following operations are still running: \n{0}", pendingJoiners.map(joiner => `- ${joiner.label}`).join('\n')) : undefined
	// 		}, () => {
	// 			return Event.toPromise(this.lifecycleService.onDidShutdown); // dismiss this dialog when we actually shutdown
	// 		}, () => {
	// 			force();
	// 		});
	// 	}, 1200);
	// 	shutdownDialogScheduler.schedule();

	// 	// Dispose scheduler when we actually shutdown
	// 	Event.once(this.lifecycleService.onDidShutdown)(() => shutdownDialogScheduler.dispose());
	// }

	// private toShutdownLabel(reason: ShutdownReason, isError: boolean): string {
	// 	if (isError) {
	// 		switch (reason) {
	// 			case ShutdownReason.CLOSE:
	// 				return localize('shutdownErrorClose', "An unexpected error prevented the window to close");
	// 			case ShutdownReason.QUIT:
	// 				return localize('shutdownErrorQuit', "An unexpected error prevented the application to quit");
	// 			case ShutdownReason.RELOAD:
	// 				return localize('shutdownErrorReload', "An unexpected error prevented the window to reload");
	// 			case ShutdownReason.LOAD:
	// 				return localize('shutdownErrorLoad', "An unexpected error prevented to change the workspace");
	// 		}
	// 	}

	// 	switch (reason) {
	// 		case ShutdownReason.CLOSE:
	// 			return localize('shutdownTitleClose', "Closing the window is taking a bit longer...");
	// 		case ShutdownReason.QUIT:
	// 			return localize('shutdownTitleQuit', "Quitting the application is taking a bit longer...");
	// 		case ShutdownReason.RELOAD:
	// 			return localize('shutdownTitleReload', "Reloading the window is taking a bit longer...");
	// 		case ShutdownReason.LOAD:
	// 			return localize('shutdownTitleLoad', "Changing the workspace is taking a bit longer...");
	// 	}
	// }

	// private toForceShutdownLabel(reason: ShutdownReason): string {
	// 	switch (reason) {
	// 		case ShutdownReason.CLOSE:
	// 			return localize('shutdownForceClose', "Close Anyway");
	// 		case ShutdownReason.QUIT:
	// 			return localize('shutdownForceQuit', "Quit Anyway");
	// 		case ShutdownReason.RELOAD:
	// 			return localize('shutdownForceReload', "Reload Anyway");
	// 		case ShutdownReason.LOAD:
	// 			return localize('shutdownForceLoad', "Change Anyway");
	// 	}
	// }

	//#endregion
}
