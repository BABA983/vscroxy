import { Disposable } from '../../../base/common/lifecycle.js';
import { IInstantiationService, ServicesAccessor } from '../../../platform/instantiation/common/instantiation.js';
import { IWhistleProcessConfiguration } from '../../../platform/whistleProcess/node/whistleProcess.js';
import { IClientConnectionFilter, Server as UtilityProcessMessagePortServer, once } from '../../../base/parts/ipc/node/ipc.mp.js';
import { IWhistleProcessLifecycleService, WhistleProcessLifecycleService } from '../../../platform/lifecycle/node/whistleProcessLifecycleService.js';
import { WhistleProcessLifecycle, WhistleProcessRawConnection } from '../../../platform/whistleProcess/common/whistleProcess.js';
import { Emitter } from '../../../base/common/event.js';
import { MessageEvent, MessagePortMain } from '../../../base/parts/sandbox/node/electronTypes.js';
import { ServiceCollection } from '../../../platform/instantiation/common/serviceCollection.js';
import { IProductService } from '../../../platform/product/common/productService.js';
import product from '../../../platform/product/common/product.js';
import { InstantiationService } from '../../../platform/instantiation/common/instantiationService.js';
import { NativeEnvironmentService } from '../../../platform/environment/node/environmentService.js';
import { INativeEnvironmentService } from '../../../platform/environment/common/environment.js';
import { LoggerChannelClient } from '../../../platform/log/common/logIpc.js';
import { ConsoleLogger, ILoggerService, ILogService, LoggerGroup } from '../../../platform/log/common/log.js';
import { localize } from '../../../nls.js';
import { LogService } from '../../../platform/log/common/logService.js';
import { URI } from '../../../base/common/uri.js';
import { StaticRouter } from '../../../base/parts/ipc/common/ipc.js';
import { IMainProcessService, MainProcessService } from '../../../platform/ipc/common/mainProcessService.js';
import { onUnexpectedError, setUnexpectedErrorHandler } from '../../../base/common/errors.js';
import { toErrorMessage } from '../../../base/common/errorMessage.js';

class WhistleProcessMain extends Disposable implements IClientConnectionFilter {

	private readonly server = this._register(new UtilityProcessMessagePortServer(this));

	private lifecycleService: WhistleProcessLifecycleService | undefined = undefined;

	private readonly onDidWindowConnectRaw = this._register(new Emitter<MessagePortMain>());

	constructor(
		private configuration: IWhistleProcessConfiguration
	) {
		super();

		// HACK
		process.env['WHISTLE_PATH'] = this.configuration.whistleAppDataPath;

		this.registerListeners();
	}

	private registerListeners() {
		let didExit = false;
		const onExit = () => {
			if (!didExit) {
				didExit = true;

				this.lifecycleService?.fireOnWillShutdown();
				this.dispose();
			}
		};
		process.once('exit', onExit);
		once(process.parentPort, WhistleProcessLifecycle.exit, onExit);
	}

	async init(): Promise<void> {
		// Services
		const instantiationService = await this.initServices();

		instantiationService.invokeFunction(accessor => {
			const logService = accessor.get(ILogService);

			// Log info
			logService.trace('whistleProcess configuration', JSON.stringify(this.configuration));

			// Channels
			this.initChannels(accessor);

			// Error handler
			this.registerErrorHandler(logService);
		});
	}

	private async initServices(): Promise<IInstantiationService> {
		const services = new ServiceCollection();

		// Product
		const productService = { _serviceBrand: undefined, ...product };
		services.set(IProductService, productService);

		// Main Process
		const mainRouter = new StaticRouter(ctx => ctx === 'main');
		const mainProcessService = new MainProcessService(this.server, mainRouter);
		services.set(IMainProcessService, mainProcessService);

		// Environment
		const environmentService = new NativeEnvironmentService(this.configuration.args, productService);
		services.set(INativeEnvironmentService, environmentService);

		// Logger
		const loggerService = new LoggerChannelClient(undefined, this.configuration.logLevel, environmentService.logsHome, this.configuration.loggers.map(loggerResource => ({ ...loggerResource, resource: URI.revive(loggerResource.resource) })), mainProcessService.getChannel('logger'));
		services.set(ILoggerService, loggerService);

		// Log
		const whistleLogGroup: LoggerGroup = { id: 'whistle', name: localize('whistleLog', "Whistle") };
		const logger = this._register(loggerService.createLogger('whistleProcess', { name: localize('whistleLog', "Whistle"), group: whistleLogGroup }));
		const consoleLogger = this._register(new ConsoleLogger(logger.getLevel()));
		const logService = this._register(new LogService(logger, [consoleLogger]));
		services.set(ILogService, logService);

		// Lifecycle
		this.lifecycleService = new WhistleProcessLifecycleService(logService);
		services.set(IWhistleProcessLifecycleService, this.lifecycleService);

		return new InstantiationService(services);
	}

	private initChannels(accessor: ServicesAccessor): void { }

	private registerErrorHandler(logService: ILogService): void {

		// Listen on global error events
		process.on('uncaughtException', error => onUnexpectedError(error));
		process.on('unhandledRejection', (reason: unknown) => onUnexpectedError(reason));

		// Register Whistle error handler
		(process as any).handleUncauthtWhistleErrorMessage = (stack: unknown, err: Error) => {
			logService.error(`[handleUncauthtWhistleErrorMessage]: ${(err && err.message) || stack}`);
		};


		// Install handler for unexpected errors
		setUnexpectedErrorHandler(error => {
			const message = toErrorMessage(error, true);
			if (!message) {
				return;
			}

			logService.error(`[uncaught exception in whistleProcess]: ${message}`);
		});
	}

	handledClientConnection(e: MessageEvent): boolean {

		// This filter on message port messages will look for
		// attempts of a window to connect raw to the whistle
		// process to handle these connections separate from
		// our IPC based protocol.

		if (e.data !== WhistleProcessRawConnection.response) {
			return false;
		}

		const port = e.ports.at(0);
		if (port) {
			this.onDidWindowConnectRaw.fire(port);

			return true;
		}

		return false;
	}
}

async function main(configuration: IWhistleProcessConfiguration) {
	try {
		const whistleProcess = new WhistleProcessMain(configuration);
		process.parentPort.postMessage(WhistleProcessLifecycle.ipcReady);
		await whistleProcess.init();
	} catch (error) {
		process.parentPort.postMessage({ error: error.toString() });
	}
}

const handle = setTimeout(() => {
	process.parentPort.postMessage({ warning: '[WhistleProcess] did not receive configuration within 30s...' });
}, 30000);

process.parentPort.once('message', (e: Electron.MessageEvent) => {
	clearTimeout(handle);
	main(e.data as IWhistleProcessConfiguration);
});
