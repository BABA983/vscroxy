import { app, dialog } from 'electron';
import { ExpectedError, setUnexpectedErrorHandler } from '../../base/common/errors.js';
import { DisposableStore } from '../../base/common/lifecycle.js';
import { IProcessEnvironment, isWindows, OS } from '../../base/common/platform.js';
import { NativeParsedArgs } from '../../platform/environment/common/argv.js';
import { EnvironmentMainService, IEnvironmentMainService } from '../../platform/environment/electron-main/environmentMainService.js';
import { parseMainProcessArgv } from '../../platform/environment/node/argvHelper.js';
import { ServiceCollection } from '../../platform/instantiation/common/serviceCollection.js';
import { ConsoleMainLogger, getLogLevel, ILoggerService, ILogService } from '../../platform/log/common/log.js';
import { LogService } from '../../platform/log/common/logService.js';
import { ILoggerMainService, LoggerMainService } from '../../platform/log/electron-main/loggerService.js';
import product from '../../platform/product/common/product.js';
import { IProductService } from '../../platform/product/common/productService.js';
import { BufferLogger } from '../../platform/log/common/bufferLog.js';
import { SyncDescriptor } from '../../platform/instantiation/common/descriptors.js';
import { ProtocolMainService } from '../../platform/protocol/electron-main/protocolMainService.js';
import { IProtocolMainService } from '../../platform/protocol/electron-main/protocol.js';
import { InstantiationService } from '../../platform/instantiation/common/instantiationService.js';
import { localize } from '../../nls.js';
import { coalesce } from '../../base/common/arrays.js';
import { toErrorMessage } from '../../base/common/errorMessage.js';
import { getPathLabel } from '../../base/common/labels.js';
import { URI } from '../../base/common/uri.js';
import { massageMessageBoxOptions } from '../../platform/dialogs/common/dialogs.js';
import { XDG_RUNTIME_DIR } from '../../base/parts/ipc/node/ipc.net.js';
import { Promises } from '../../base/common/async.js';
import { addUNCHostToAllowlist, getUNCHost } from '../../base/node/unc.js';
import { Schemas } from '../../base/common/network.js';
import { promises } from 'fs';
import { ServicesAccessor } from '../../platform/instantiation/common/instantiation.js';
import { ProxyApplication } from './app.js';
import { ILifecycleMainService, LifecycleMainService } from '../../platform/lifecycle/electron-main/lifecycleMainService.js';

class AppMain {
	main(): void {
		try {
			this.startup();
		} catch (error) {
			console.error(error.message);
			app.exit(1);
		}
	}

	private async startup(): Promise<void> {
		// Set the error handler early enough so that we are not getting the
		// default electron error dialog popping up
		setUnexpectedErrorHandler(err => console.error(err));

		// Create services
		const { instantiationService, productService, environmentMainService, bufferLogger, instanceEnvironment } = this.createServices();

		try {
			// Init services
			try {
				await this.initService(environmentMainService);
			} catch (error) {
				// Show a dialog for errors that can be resolved by the user
				this.handleStartupDataDirError(environmentMainService, productService, error);

				throw error;
			}

			// Startup
			await instantiationService.invokeFunction(async accessor => {
				const logService = accessor.get(ILogService);
				const loggerService = accessor.get(ILoggerService);

				logService.info('Startup...');

				await this.claimInstance(logService);

				// Delay creation of spdlog for perf reasons (https://github.com/microsoft/vscode/issues/72906)
				bufferLogger.logger = loggerService.createLogger('main', { name: localize('mainLog', "Main") });

				return instantiationService.createInstance(ProxyApplication, instanceEnvironment).startup();
			});
		} catch (error) {
			instantiationService.invokeFunction(this.quit, error);
		}
	}

	private createServices() {
		const services = new ServiceCollection();
		const disposables = new DisposableStore();
		process.once('exit', () => disposables.dispose());

		// Product
		const productService = { _serviceBrand: undefined, ...product };
		services.set(IProductService, productService);

		// Environment
		const environmentMainService = new EnvironmentMainService(this.resolveArgs(), productService);
		const instanceEnvironment = this.patchEnvironment(environmentMainService); // Patch `process.env` with the instance's environment
		services.set(IEnvironmentMainService, environmentMainService);

		// Logger
		const loggerService = new LoggerMainService(getLogLevel(environmentMainService), environmentMainService.logsHome);
		services.set(ILoggerMainService, loggerService);

		// Log: We need to buffer the spdlog logs until we are sure
		// we are the only instance running, otherwise we'll have concurrent
		// log file access on Windows (https://github.com/microsoft/vscode/issues/41218)
		const bufferLogger = new BufferLogger(loggerService.getLogLevel());
		const logService = disposables.add(new LogService(bufferLogger, [new ConsoleMainLogger(loggerService.getLogLevel())]));
		services.set(ILogService, logService);

		// Lifecycle
		services.set(ILifecycleMainService, new SyncDescriptor(LifecycleMainService, undefined, false));

		// Protocol (instantiated early and not using sync descriptor for security reasons)
		services.set(IProtocolMainService, new ProtocolMainService(environmentMainService, logService));

		const instantiationService = new InstantiationService(services, true);

		return {
			instantiationService,
			instanceEnvironment,
			productService,
			environmentMainService,
			bufferLogger
		};
	}

	private patchEnvironment(environmentMainService: IEnvironmentMainService): IProcessEnvironment {
		const instanceEnvironment: IProcessEnvironment = {};

		['VSCODE_NLS_CONFIG'].forEach(key => {
			const value = process.env[key];
			if (typeof value === 'string') {
				instanceEnvironment[key] = value;
			}
		});

		Object.assign(process.env, instanceEnvironment);

		return instanceEnvironment;
	}

	private async initService(environmentMainService: IEnvironmentMainService): Promise<void> {
		await Promises.settled<unknown>([

			// Environment service (paths)
			Promise.all<string | undefined>([
				this.allowWindowsUNCPath(environmentMainService.extensionsPath), // enable extension paths on UNC drives...
				environmentMainService.logsHome.with({ scheme: Schemas.file }).fsPath,
			].map(path => path ? promises.mkdir(path, { recursive: true }) : undefined)),

			// TODO: State service

			// TODO: Configuration service
		]);
	}

	private allowWindowsUNCPath(path: string): string {
		if (isWindows) {
			const host = getUNCHost(path);
			if (host) {
				addUNCHostToAllowlist(host);
			}
		}

		return path;
	}

	private async claimInstance(logService: ILogService) {
		const lock = app.requestSingleInstanceLock();
		if (!lock) {
			logService.error('App is already running...');
			app.exit(0);
			return;
		}
	}


	private handleStartupDataDirError(environmentMainService: IEnvironmentMainService, productService: IProductService, error: NodeJS.ErrnoException): void {
		if (error.code === 'EACCES' || error.code === 'EPERM') {
			const directories = coalesce([environmentMainService.userDataPath, environmentMainService.extensionsPath, XDG_RUNTIME_DIR]).map(folder => getPathLabel(URI.file(folder), { os: OS, tildify: environmentMainService }));

			this.showStartupWarningDialog(
				localize('startupDataDirError', "Unable to write program user data."),
				localize('startupUserDataAndExtensionsDirErrorDetail', "{0}\n\nPlease make sure the following directories are writeable:\n\n{1}", toErrorMessage(error), directories.join('\n')),
				productService
			);
		}
	}

	private showStartupWarningDialog(message: string, detail: string, productService: IProductService): void {

		// use sync variant here because we likely exit after this method
		// due to startup issues and otherwise the dialog seems to disappear
		// https://github.com/microsoft/vscode/issues/104493

		dialog.showMessageBoxSync(massageMessageBoxOptions({
			type: 'warning',
			buttons: [localize({ key: 'close', comment: ['&& denotes a mnemonic'] }, "&&Close")],
			message,
			detail
		}, productService).options);
	}

	private quit(accessor: ServicesAccessor, reason?: ExpectedError | Error): void {
		const logService = accessor.get(ILogService);
		const lifecycleMainService = accessor.get(ILifecycleMainService);

		let exitCode = 0;

		if (reason) {
			if ((reason as ExpectedError).isExpected) {
				if (reason.message) {
					logService.trace(reason.message);
				}
			} else {
				exitCode = 1; // signal error to the outside

				if (reason.stack) {
					logService.error(reason.stack);
				} else {
					logService.error(`Startup error: ${reason.toString()}`);
				}
			}
		}

		lifecycleMainService.kill(exitCode);
	}

	//#region Command line arguments utilities
	private resolveArgs(): NativeParsedArgs {
		const args = parseMainProcessArgv(process.argv);
		return args;
	}
	//#endregion


}

const appMain = new AppMain();
appMain.main();


