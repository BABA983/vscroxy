import { IpcMainEvent, MessagePortMain } from 'electron';
import { Barrier, DeferredPromise } from '../../../base/common/async.js';
import { Emitter } from '../../../base/common/event.js';
import { Disposable, IDisposable } from '../../../base/common/lifecycle.js';
import { assertIsDefined } from '../../../base/common/types.js';
import { validatedIpcMain } from '../../../base/parts/ipc/electron-main/ipcMain.js';
import { IEnvironmentMainService } from '../../environment/electron-main/environmentMainService.js';
import { parseWhistleProcessDebugPort } from '../../environment/node/environmentService.js';
import { ILifecycleMainService } from '../../lifecycle/electron-main/lifecycleMainService.js';
import { ILogService } from '../../log/common/log.js';
import { ILoggerMainService } from '../../log/electron-main/loggerService.js';
import { UtilityProcess } from '../../utilityProcess/electron-main/utilityProcess.js';
import { WhistleProcessChannelConnection, WhistleProcessLifecycle, WhistleProcessRawConnection } from '../common/whistleProcess.js';
import { IWhistleProcessConfiguration } from '../node/whistleProcess.js';

export class WhistleProcess extends Disposable {

	private readonly firstWindowConnectionBarrier = new Barrier();

	private utilityProcess: UtilityProcess | undefined = undefined;
	private utilityProcessLogListener: IDisposable | undefined = undefined;

	private readonly _onDidCrash = this._register(new Emitter<void>());
	readonly onDidCrash = this._onDidCrash.event;

	constructor(
		@IEnvironmentMainService private readonly environmentMainService: IEnvironmentMainService,
		@ILifecycleMainService private readonly lifecycleMainService: ILifecycleMainService,
		@ILogService private readonly logService: ILogService,
		@ILoggerMainService private readonly loggerMainService: ILoggerMainService,
	) {
		super();

		this.registerListeners();
	}

	private registerListeners() {
		// whistle process channel connections from workbench windows
		validatedIpcMain.on(WhistleProcessChannelConnection.request, (e, nonce: string) => this.onWindowConnection(e, nonce, WhistleProcessChannelConnection.response));

		// whistle process raw connections from workbench windows
		validatedIpcMain.on(WhistleProcessRawConnection.request, (e, nonce: string) => this.onWindowConnection(e, nonce, WhistleProcessRawConnection.response));

		this.lifecycleMainService.onWillShutdown(() => this.onWillShutdown());
	}

	private async onWindowConnection(e: IpcMainEvent, nonce: string, responseChannel: string): Promise<void> {
		this.logService.trace(`[WhistleProcess] onWindowConnection for: ${responseChannel}`);

		// release barrier if this is the first window connection
		if (!this.firstWindowConnectionBarrier.isOpen()) {
			this.firstWindowConnectionBarrier.open();
		}

		// await the whistle process to be overall ready
		// we do not just wait for IPC ready because the
		// workbench window will communicate directly

		await this.whenReady();

		// connect to the whistle process passing the responseChannel
		// as payload to give a hint what the connection is about

		const port = await this.connect(responseChannel);

		// Check back if the requesting window meanwhile closed
		// Since whistle process is delayed on startup there is
		// a chance that the window close before the whistle process
		// was ready for a connection.

		if (e.sender.isDestroyed()) {
			return port.close();
		}

		// send the port back to the requesting window
		e.sender.postMessage(responseChannel, nonce, [port]);
	}

	private onWillShutdown(): void {
		this.logService.trace('[WhistleProcess] onWillShutdown');

		this.utilityProcess?.postMessage(WhistleProcessLifecycle.exit);
		this.utilityProcess = undefined;
	}

	private _whenReady: Promise<void> | undefined = undefined;
	whenReady(): Promise<void> {
		if (!this._whenReady) {
			this._whenReady = (async () => {

				// Wait for whistle process being ready to accept connection
				await this.whenIpcReady;

				// Overall signal that the whistle process was loaded and
				// all services within have been created.

				const whenReady = new DeferredPromise<void>();
				this.utilityProcess?.once(WhistleProcessLifecycle.initDone, () => whenReady.complete());

				await whenReady.p;
				this.utilityProcessLogListener?.dispose();
				this.logService.trace('[WhistleProcess] Overall ready');
			})();
		}

		return this._whenReady;
	}

	private _whenIpcReady: Promise<void> | undefined = undefined;
	private get whenIpcReady() {
		if (!this._whenIpcReady) {
			this._whenIpcReady = (async () => {

				// Always wait for first window asking for connection
				await this.firstWindowConnectionBarrier.wait();

				// Spawn whistle process
				this.createUtilityProcess();

				// Wait for whistle process indicating that IPC connections are accepted
				const WhistleProcessIpcReady = new DeferredPromise<void>();
				this.utilityProcess?.once(WhistleProcessLifecycle.ipcReady, () => WhistleProcessIpcReady.complete());

				await WhistleProcessIpcReady.p;
				this.logService.trace('[WhistleProcess] IPC ready');
			})();
		}

		return this._whenIpcReady;
	}

	private createUtilityProcess(): void {
		this.utilityProcess = this._register(new UtilityProcess(this.logService, this.lifecycleMainService));

		// Install a log listener for very early whistle process warnings and errors
		this.utilityProcessLogListener = this.utilityProcess.onMessage((e: any) => {
			if (typeof e.warning === 'string') {
				this.logService.warn(e.warning);
			} else if (typeof e.error === 'string') {
				this.logService.error(e.error);
			}
		});

		const inspectParams = parseWhistleProcessDebugPort(this.environmentMainService.args, this.environmentMainService.isBuilt);
		let execArgv: string[] | undefined = undefined;
		if (inspectParams.port) {
			execArgv = ['--nolazy'];
			if (inspectParams.break) {
				execArgv.push(`--inspect-brk=${inspectParams.port}`);
			} else {
				execArgv.push(`--inspect=${inspectParams.port}`);
			}
		}

		this.utilityProcess.start({
			type: 'whistle-process',
			entryPoint: 'vs/croxy/electron-utility/whistleProcess/whistleProcessMain',
			payload: this.createWhistleProcessConfiguration(),
			respondToAuthRequestsFromMainProcess: true,
			execArgv
		});

		this._register(this.utilityProcess.onCrash(() => this._onDidCrash.fire()));
	}

	private createWhistleProcessConfiguration(): IWhistleProcessConfiguration {
		return {
			args: this.environmentMainService.args,
			logLevel: this.loggerMainService.getLogLevel(),
			loggers: this.loggerMainService.getGlobalLoggers(),
			whistleAppDataPath: this.environmentMainService.whistleAppDataPath.fsPath
		};
	}

	async connect(payload?: unknown): Promise<MessagePortMain> {

		// Wait for whistle process being ready to accept connection
		await this.whenIpcReady;

		// Connect and return message port
		const utilityProcess = assertIsDefined(this.utilityProcess);
		return utilityProcess.connect(payload);
	}
}
