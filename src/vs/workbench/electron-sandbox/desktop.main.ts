import { setFullscreen } from '../../base/browser/browser.js';
import { domContentLoaded } from '../../base/browser/dom.js';
import { mainWindow } from '../../base/browser/window.js';
import { onUnexpectedError } from '../../base/common/errors.js';
import { Disposable } from '../../base/common/lifecycle.js';
import { safeStringify } from '../../base/common/objects.js';
import { isBigSurOrNewer, isCI, isMacintosh } from '../../base/common/platform.js';
import { URI } from '../../base/common/uri.js';
import { ServiceCollection } from '../../platform/instantiation/common/serviceCollection.js';
import { IMainProcessService } from '../../platform/ipc/common/mainProcessService.js';
import { ElectronIPCMainProcessService } from '../../platform/ipc/electron-sandbox/mainProcessService.js';
import { ILoggerService, ILogService, LogLevel } from '../../platform/log/common/log.js';
import { LoggerChannelClient } from '../../platform/log/common/logIpc.js';
import product from '../../platform/product/common/product.js';
import { IProductService } from '../../platform/product/common/productService.js';
import { IStorageService } from '../../platform/storage/common/storage.js';
import { IUserDataProfilesService, reviveProfile } from '../../platform/userDataProfile/common/userDataProfile.js';
import { UserDataProfilesService } from '../../platform/userDataProfile/common/userDataProfileIpc.js';
import { INativeWindowConfiguration } from '../../platform/window/common/window.js';
import { IAnyWorkspaceIdentifier, UNKNOWN_EMPTY_WINDOW_WORKSPACE } from '../../platform/workspace/common/workspace.js';
import { Workbench } from '../browser/workbench.js';
import { WorkbenchConfigurationService } from '../services/configuration/browser/configurationService.js';
import { IWorkbenchConfigurationService } from '../services/configuration/common/configuration.js';
import { INativeWorkbenchEnvironmentService, NativeWorkbenchEnvironmentService } from '../services/environment/electron-sandbox/environmentService.js';
import { NativeLogService } from '../services/log/electron-sandbox/logService.js';
import { BrowserStorageService } from '../services/storage/browser/storageService.js';
import { IUserDataProfileService } from '../services/userDataProfile/common/userDataProfile.js';
import { UserDataProfileService } from '../services/userDataProfile/common/userDataProfileService.js';

export class DesktopMain extends Disposable {
	constructor(private readonly configuration: INativeWindowConfiguration) {
		super();
	}

	private init(): void {
		// Apply fullscreen early if configured
		setFullscreen(!!this.configuration.fullscreen, mainWindow);
	}

	async open(): Promise<void> {

		// Init services and wait for DOM to be ready in parallel
		const [services] = await Promise.all([this.initServices(), domContentLoaded(mainWindow)]);

		// Create Workbench
		const workbench = new Workbench(mainWindow.document.body, { extraClasses: this.getExtraClasses() }, services.serviceCollection, services.logService);

		// Startup
		const instantiationService = workbench.startup();
	}

	private getExtraClasses(): string[] {
		if (isMacintosh && isBigSurOrNewer(this.configuration.os.release)) {
			return ['macos-bigsur-or-newer'];
		}

		return [];
	}

	private async initServices() {
		const serviceCollection = new ServiceCollection();

		// !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
		//
		// NOTE: Please do NOT register services here. Use `registerSingleton()`
		//       from `workbench.common.main.ts` if the service is shared between
		//       desktop and web or `workbench.desktop.main.ts` if the service
		//       is desktop only.
		//
		// !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!


		// Main Process
		const mainProcessService = this._register(new ElectronIPCMainProcessService(this.configuration.windowId));
		serviceCollection.set(IMainProcessService, mainProcessService);

		// Product
		const productService: IProductService = { _serviceBrand: undefined, ...product };
		serviceCollection.set(IProductService, productService);

		// Environment
		const environmentService = new NativeWorkbenchEnvironmentService(this.configuration, productService);
		serviceCollection.set(INativeWorkbenchEnvironmentService, environmentService);

		// Logger
		const loggers = [
			...this.configuration.loggers.global.map(loggerResource => ({ ...loggerResource, resource: URI.revive(loggerResource.resource) })),
			...this.configuration.loggers.window.map(loggerResource => ({ ...loggerResource, resource: URI.revive(loggerResource.resource), hidden: true })),
		];
		const loggerService = new LoggerChannelClient(this.configuration.windowId, this.configuration.logLevel, environmentService.windowLogsPath, loggers, mainProcessService.getChannel('logger'));
		serviceCollection.set(ILoggerService, loggerService);

		// Log
		const logService = this._register(new NativeLogService(loggerService, environmentService));
		serviceCollection.set(ILogService, logService);
		if (isCI) {
			logService.info('workbench#open()'); // marking workbench open helps to diagnose flaky integration/smoke tests
		}
		if (logService.getLevel() === LogLevel.Trace) {
			logService.trace('workbench#open(): with configuration', safeStringify({ ...this.configuration, nls: undefined /* exclude large property */ }));
		}

		// User Data Profiles
		const userDataProfilesService = new UserDataProfilesService(this.configuration.profiles.all, URI.revive(this.configuration.profiles.home).with({ scheme: environmentService.userRoamingDataHome.scheme }), mainProcessService.getChannel('userDataProfiles'));
		serviceCollection.set(IUserDataProfilesService, userDataProfilesService);
		const userDataProfileService = new UserDataProfileService(reviveProfile(this.configuration.profiles.profile, userDataProfilesService.profilesHome.scheme));
		serviceCollection.set(IUserDataProfileService, userDataProfileService);

		const workspace = this.resolveWorkspaceIdentifier(environmentService);
		await Promise.all([
			this.createWorkbenchConfigurationService(workspace).then(service => {
				// Storage
				serviceCollection.set(IWorkbenchConfigurationService, service);

				return service;
			}),
			this.createStorageService(workspace, userDataProfileService, logService).then(service => {
				// Storage
				serviceCollection.set(IStorageService, service);

				return service;
			})
		])


		// !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
		//
		// NOTE: Please do NOT register services here. Use `registerSingleton()`
		//       from `workbench.common.main.ts` if the service is shared between
		//       desktop and web or `workbench.desktop.main.ts` if the service
		//       is desktop only.
		//
		// !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!

		return { serviceCollection, logService };
	}

	private async createWorkbenchConfigurationService(workspace: IAnyWorkspaceIdentifier) {
		const workbenchConfigurationService = new WorkbenchConfigurationService();

		try {
			await workbenchConfigurationService.initialize(workspace);

			return workbenchConfigurationService;
		} catch (error) {
			onUnexpectedError(error);

			return workbenchConfigurationService;
		}
	}

	private async createStorageService(workspace: IAnyWorkspaceIdentifier, userDataProfileService: IUserDataProfileService, logService: ILogService) {
		const storageService = new BrowserStorageService(workspace, userDataProfileService, logService);
		try {
			await storageService.initialize();

			return storageService;
		} catch (error) {
			onUnexpectedError(error);

			return storageService;
		}
	}

	private resolveWorkspaceIdentifier(environmentService: INativeWorkbenchEnvironmentService): IAnyWorkspaceIdentifier {

		return UNKNOWN_EMPTY_WINDOW_WORKSPACE;
	}
}

export interface IDesktopMain {
	main(configuration: INativeWindowConfiguration): Promise<void>;
}

export function main(configuration: INativeWindowConfiguration): Promise<void> {
	const workbench = new DesktopMain(configuration);

	return workbench.open();
}
