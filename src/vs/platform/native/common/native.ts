/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { Event } from '../../../base/common/event.js';
import { createDecorator } from '../../instantiation/common/instantiation.js';

export interface ICPUProperties {
	model: string;
	speed: number;
}

export interface IOSProperties {
	type: string;
	release: string;
	arch: string;
	platform: string;
	cpus: ICPUProperties[];
}

export interface IOSStatistics {
	totalmem: number;
	freemem: number;
	loadavg: number[];
}

export interface INativeHostOptions {
	readonly targetWindowId?: number;
}

export interface ICommonNativeHostService {

	readonly _serviceBrand: undefined;

	// Properties
	readonly windowId: number;

	// Lifecycle
	notifyReady(): Promise<void>;
	relaunch(options?: { addArgs?: string[]; removeArgs?: string[] }): Promise<void>;
	reload(options?: { disableExtensions?: boolean }): Promise<void>;
	closeWindow(options?: INativeHostOptions): Promise<void>;
	quit(): Promise<void>;
	exit(code: number): Promise<void>;

}

export const INativeHostService = createDecorator<INativeHostService>('nativeHostService');

/**
 * A set of methods specific to a native host, i.e. unsupported in web
 * environments.
 *
 * @see {@link IHostService} for methods that can be used in native and web
 * hosts.
 */
export interface INativeHostService extends ICommonNativeHostService {
	readonly _serviceBrand: undefined;

	getActiveWindowId(): Promise<number | undefined>;

	readonly onDidChangeWindowFullScreen: Event<{ windowId: number; fullscreen: boolean }>;

	readonly onDidFocusMainOrAuxiliaryWindow: Event<number>;
	readonly onDidBlurMainOrAuxiliaryWindow: Event<number>;
}
