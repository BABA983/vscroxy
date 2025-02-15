import '../base/common/codicons.js'
import '../base/browser/ui/codicons/codiconStyles.js'

//#region --- workbench actions

import './browser/actions/quickAccessActions.js';

//#endregion

//#region --- workbench parts

import './browser/parts/paneCompositePartService.js';
import './browser/parts/statusbar/statusbarPart.js';
// import './browser/parts/editor/editorParts2.js';

//#endregion


//#region --- workbench services

import '../platform/actions/common/actions.contribution.js';
import './services/themes/browser/workbenchThemeService.js';
import './services/keybinding/browser/keybindingService.js';
import './services/commands/common/commandService.js';
import './services/notification/common/notificationService.js';
import '../editor/browser/services/hoverService/hoverService.js';
import './services/views/browser/viewDescriptorService.js';
import './services/views/browser/viewsService.js';
import './services/activity/browser/activityService.js';

import { InstantiationType, registerSingleton } from '../platform/instantiation/common/extensions.js';
import { ContextKeyService } from '../platform/contextkey/browser/contextKeyService.js';
import { IContextKeyService } from '../platform/contextkey/common/contextkey.js';
import { ContextViewService } from '../platform/contextview/browser/contextViewService.js';
import { IContextViewService } from '../platform/contextview/browser/contextView.js';
import { OpenerService } from '../editor/browser/services/openerService.js';
import { IOpenerService } from '../platform/opener/common/opener.js';

registerSingleton(IContextKeyService, ContextKeyService, InstantiationType.Delayed);
registerSingleton(IContextViewService, ContextViewService, InstantiationType.Delayed);
registerSingleton(IOpenerService, OpenerService, InstantiationType.Delayed);


//#endregion


//#region --- workbench contributions

// Network Traffic
import './contrib/networkTraffic/browser/networkTraffic.contribution.js';

//#endregion
