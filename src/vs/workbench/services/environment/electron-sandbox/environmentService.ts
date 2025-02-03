/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { VSBuffer } from '../../../../base/common/buffer.js';
import { memoize } from '../../../../base/common/decorators.js';
import { Schemas } from '../../../../base/common/network.js';
import { PerformanceMark } from '../../../../base/common/performance.js';
import { joinPath } from '../../../../base/common/resources.js';
import { URI } from '../../../../base/common/uri.js';
import { IEnvironmentService, INativeEnvironmentService } from '../../../../platform/environment/common/environment.js';
import { AbstractNativeEnvironmentService } from '../../../../platform/environment/common/environmentService.js';
import { refineServiceDecorator } from '../../../../platform/instantiation/common/instantiation.js';
import { IProductService } from '../../../../platform/product/common/productService.js';
import { IColorScheme, INativeWindowConfiguration, IOSConfiguration } from '../../../../platform/window/common/window.js';
import { IBrowserWorkbenchEnvironmentService } from '../browser/environmentService.js';

export const INativeWorkbenchEnvironmentService = refineServiceDecorator<IEnvironmentService, INativeWorkbenchEnvironmentService>(IEnvironmentService);

/**
 * A subclass of the `IWorkbenchEnvironmentService` to be used only in native
 * environments (Windows, Linux, macOS) but not e.g. web.
 */
export interface INativeWorkbenchEnvironmentService extends IBrowserWorkbenchEnvironmentService, INativeEnvironmentService {

	// --- Window
	readonly window: {
		id: number;
		handle?: VSBuffer;
		colorScheme: IColorScheme;
		maximized?: boolean;
		accessibilitySupport?: boolean;
		isInitialStartup?: boolean;
		isCodeCaching?: boolean;
		perfMarks: PerformanceMark[];
	};

	// --- Main
	readonly mainPid: number;
	readonly os: IOSConfiguration;

	// --- Paths
	readonly execPath: string;

	// --- Development
	readonly crashReporterDirectory?: string;
	readonly crashReporterId?: string;

}

export class NativeWorkbenchEnvironmentService extends AbstractNativeEnvironmentService implements INativeWorkbenchEnvironmentService {

	@memoize
	get mainPid() { return this.configuration.mainPid; }

	@memoize
	get execPath() { return this.configuration.execPath; }

	@memoize
	get window() {
		return {
			id: this.configuration.windowId,
			handle: this.configuration.handle,
			colorScheme: this.configuration.colorScheme,
			maximized: this.configuration.maximized,
			accessibilitySupport: this.configuration.accessibilitySupport,
			perfMarks: this.configuration.perfMarks,
			isInitialStartup: this.configuration.isInitialStartup,
			isCodeCaching: typeof this.configuration.codeCachePath === 'string'
		};
	}

	@memoize
	get windowLogsPath(): URI { return joinPath(this.logsHome, `window${this.configuration.windowId}`); }

	@memoize
	get logFile(): URI { return joinPath(this.windowLogsPath, `renderer.log`); }

	@memoize
	get extHostLogsPath(): URI { return joinPath(this.windowLogsPath, 'exthost'); }

	@memoize
	get extHostTelemetryLogFile(): URI {
		return joinPath(this.extHostLogsPath, 'extensionTelemetry.log');
	}

	@memoize
	get webviewExternalEndpoint(): string { return `${Schemas.vscodeWebview}://{{uuid}}`; }

	@memoize
	get skipReleaseNotes(): boolean { return !!this.args['skip-release-notes']; }

	@memoize
	get skipWelcome(): boolean { return !!this.args['skip-welcome']; }

	@memoize
	get logExtensionHostCommunication(): boolean { return !!this.args.logExtensionHostCommunication; }

	@memoize
	get enableSmokeTestDriver(): boolean { return !!this.args['enable-smoke-test-driver']; }

	@memoize
	get extensionEnabledProposedApi(): string[] | undefined {
		if (Array.isArray(this.args['enable-proposed-api'])) {
			return this.args['enable-proposed-api'];
		}

		if ('enable-proposed-api' in this.args) {
			return [];
		}

		return undefined;
	}

	@memoize
	get os(): IOSConfiguration { return this.configuration.os; }

	constructor(
		private readonly configuration: INativeWindowConfiguration,
		productService: IProductService
	) {
		super(configuration, { homeDir: configuration.homeDir, tmpDir: configuration.tmpDir, userDataDir: configuration.userDataDir }, productService);
	}

}
