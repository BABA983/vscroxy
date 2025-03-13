import { Barrier, timeout } from '../../../../base/common/async.js';
import { Disposable } from '../../../../base/common/lifecycle.js';
import { mark } from '../../../../base/common/performance.js';
import { IChannel, IServerChannel, getDelayedChannel } from '../../../../base/parts/ipc/common/ipc.js';
import { Client as MessagePortClient } from '../../../../base/parts/ipc/common/ipc.mp.js';
import { acquirePort } from '../../../../base/parts/ipc/electron-sandbox/ipc.mp.js';
import { IWhistleProcessService } from '../../../../platform/ipc/electron-sandbox/services.js';
import { ILogService } from '../../../../platform/log/common/log.js';
import { WhistleProcessChannelConnection, WhistleProcessRawConnection } from '../../../../platform/whistleProcess/common/whistleProcess.js';

export class WhistleProcessService extends Disposable implements IWhistleProcessService {

	declare readonly _serviceBrand: undefined;

	private readonly withWhistleProcessConnection: Promise<MessagePortClient>;

	private readonly restoredBarrier = new Barrier();

	constructor(
		readonly windowId: number,
		@ILogService private readonly logService: ILogService
	) {
		super();

		this.withWhistleProcessConnection = this.connect();
	}

	private async connect(): Promise<MessagePortClient> {
		this.logService.trace('Renderer->WhistleProcess#connect');

		await Promise.race([this.restoredBarrier.wait(), timeout(2000)]);

		// Acquire a message port connected to the whistle process
		mark('code/willConnectWhistleProcess');
		this.logService.trace('Renderer->WhistleProcess#connect: before acquirePort');
		const port = await acquirePort(WhistleProcessChannelConnection.request, WhistleProcessChannelConnection.response);
		mark('code/didConnectWhistleProcess');
		this.logService.trace('Renderer->WhistleProcess#connect: connection established');

		return this._register(new MessagePortClient(port, `window:${this.windowId}`));
	}

	notifyRestored(): void {
		if (!this.restoredBarrier.isOpen()) {
			this.restoredBarrier.open();
		}
	}

	getChannel(channelName: string): IChannel {
		return getDelayedChannel(this.withWhistleProcessConnection.then(connection => connection.getChannel(channelName)));
	}

	registerChannel(channelName: string, channel: IServerChannel<string>): void {
		this.withWhistleProcessConnection.then(connection => connection.registerChannel(channelName, channel));
	}

	async createRawConnection(): Promise<MessagePort> {

		// Await initialization of the whistle process
		await this.withWhistleProcessConnection;

		// Create a new port to the whistle process
		this.logService.trace('Renderer->WhistleProcess#createRawConnection: before acquirePort');
		const port = await acquirePort(WhistleProcessRawConnection.request, WhistleProcessRawConnection.response);
		this.logService.trace('Renderer->WhistleProcess#createRawConnection: connection established');

		return port;
	}

}
