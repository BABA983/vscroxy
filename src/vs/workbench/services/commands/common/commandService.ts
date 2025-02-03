/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { Emitter, Event } from '../../../../base/common/event.js';
import { Disposable } from '../../../../base/common/lifecycle.js';
import { ICommandEvent, ICommandService } from '../../../../platform/commands/common/commands.js';
import { InstantiationType, registerSingleton } from '../../../../platform/instantiation/common/extensions.js';

export class CommandService extends Disposable implements ICommandService {
	executeCommand<T = any>(commandId: string, ...args: any[]): Promise<T | undefined> {
		throw new Error('Method not implemented.');
	}

	declare readonly _serviceBrand: undefined;

	private _extensionHostIsReady: boolean = false;
	private _starActivation: Promise<void> | null;

	private readonly _onWillExecuteCommand: Emitter<ICommandEvent> = this._register(new Emitter<ICommandEvent>());
	public readonly onWillExecuteCommand: Event<ICommandEvent> = this._onWillExecuteCommand.event;

	private readonly _onDidExecuteCommand: Emitter<ICommandEvent> = new Emitter<ICommandEvent>();
	public readonly onDidExecuteCommand: Event<ICommandEvent> = this._onDidExecuteCommand.event;


}

registerSingleton(ICommandService, CommandService, InstantiationType.Delayed);
