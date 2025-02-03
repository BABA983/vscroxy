/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/


// #######################################################################
// ###                                                                 ###
// ### !!! PLEASE ADD COMMON IMPORTS INTO WORKBENCH.COMMON.MAIN.TS !!! ###
// ###                                                                 ###
// #######################################################################

//#region --- workbench common

import './workbench.common.main.js';

//#endregion


//#region --- workbench (desktop main)

import './electron-sandbox/desktop.main.js';

//#endregion


//#region --- workbench parts


//#endregion


//#region --- workbench services

import './services/lifecycle/electron-sandbox/lifecycleService.js';
import './services/host/electron-sandbox/nativeHostService.js';
import './services/title/electron-sandbox/titleService.js';
import './services/contextmenu/electron-sandbox/contextmenuService.js';
import './services/accessibility/electron-sandbox/accessibilityService.js';
import '../platform/userDataProfile/electron-sandbox/userDataProfileStorageService.js';

//#endregion


//#region --- workbench contributions

//#endregion


export { main } from './electron-sandbox/desktop.main.js';
