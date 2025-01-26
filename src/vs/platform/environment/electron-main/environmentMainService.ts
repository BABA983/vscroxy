/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { IEnvironmentService, INativeEnvironmentService } from '../common/environment.js';
import { NativeEnvironmentService } from '../node/environmentService.js';
import { refineServiceDecorator } from '../../instantiation/common/instantiation.js';

export const IEnvironmentMainService = refineServiceDecorator<IEnvironmentService, IEnvironmentMainService>(IEnvironmentService);

/**
 * A subclass of the `INativeEnvironmentService` to be used only in electron-main
 * environments.
 */
export interface IEnvironmentMainService extends INativeEnvironmentService { }

export class EnvironmentMainService extends NativeEnvironmentService implements IEnvironmentMainService { }
