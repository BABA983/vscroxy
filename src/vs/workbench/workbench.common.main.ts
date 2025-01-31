//#region --- workbench actions

import './browser/actions/quickAccessActions.js';

//#endregion

//#region --- workbench parts

// import './browser/parts/paneCompositePartService.js';

//#endregion


//#region --- workbench services

import '../platform/actions/common/actions.contribution.js';
import './services/themes/browser/workbenchThemeService.js';
import './services/keybinding/browser/keybindingService.js';
import './services/commands/common/commandService.js';
import './services/notification/common/notificationService.js';

import { InstantiationType, registerSingleton } from '../platform/instantiation/common/extensions.js';
import { ContextKeyService } from '../platform/contextkey/browser/contextKeyService.js';
import { IContextKeyService } from '../platform/contextkey/common/contextkey.js';

registerSingleton(IContextKeyService, ContextKeyService, InstantiationType.Delayed);

//#endregion


