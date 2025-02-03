/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { IContextMenuDelegate } from '../../../../base/browser/contextmenu.js';
import { Event } from '../../../../base/common/event.js';
import { Disposable } from '../../../../base/common/lifecycle.js';
import { IMenuService } from '../../../../platform/actions/common/actions.js';
import { IConfigurationService } from '../../../../platform/configuration/common/configuration.js';
import { IContextKeyService } from '../../../../platform/contextkey/common/contextkey.js';
import { IContextMenuMenuDelegate, IContextMenuService } from '../../../../platform/contextview/browser/contextView.js';
import { InstantiationType, registerSingleton } from '../../../../platform/instantiation/common/extensions.js';
import { IKeybindingService } from '../../../../platform/keybinding/common/keybinding.js';

export class ContextMenuService implements IContextMenuService {

	declare readonly _serviceBrand: undefined;

	private impl: any;

	get onDidShowContextMenu(): Event<void> { return this.impl.onDidShowContextMenu; }
	get onDidHideContextMenu(): Event<void> { return this.impl.onDidHideContextMenu; }

	constructor(
		@IKeybindingService keybindingService: IKeybindingService,
		@IConfigurationService configurationService: IConfigurationService,
		@IMenuService menuService: IMenuService,
		@IContextKeyService contextKeyService: IContextKeyService,
	) {


	}

	dispose(): void {
	}

	showContextMenu(delegate: IContextMenuDelegate | IContextMenuMenuDelegate): void {
	}
}

class NativeContextMenuService extends Disposable implements IContextMenuService {
	_serviceBrand: undefined;
	onDidShowContextMenu: Event<void>;
	onDidHideContextMenu: Event<void>;
	showContextMenu(delegate: IContextMenuDelegate | IContextMenuMenuDelegate): void {
		throw new Error('Method not implemented.');
	}


}

registerSingleton(IContextMenuService, ContextMenuService, InstantiationType.Delayed);
