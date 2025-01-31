import { CodeWindow, mainWindow } from '../../../../base/browser/window.js';
import { IInstantiationService } from '../../../../platform/instantiation/common/instantiation.js';
import { InstantiationService } from '../../../../platform/instantiation/common/instantiationService.js';
import { IStorageService } from '../../../../platform/storage/common/storage.js';
import { IThemeService } from '../../../../platform/theme/common/themeService.js';
import { BrowserTitlebarPart, BrowserTitleService } from '../../../browser/parts/titlebar/titlebarPart.js'
import { IWorkbenchLayoutService, Parts } from '../../../services/layout/browser/layoutService.js';

export class NativeTitlebarPart extends BrowserTitlebarPart {
	constructor(
		id: string,
		targetWindow: CodeWindow,
		editorGroupsContainer: 'main',
		@IThemeService themeService: IThemeService,
		@IStorageService storageService: IStorageService,
		@IWorkbenchLayoutService layoutService: IWorkbenchLayoutService,
		@IInstantiationService instantiationService: IInstantiationService
	) {
		super(id, targetWindow, editorGroupsContainer, themeService, storageService, layoutService, instantiationService);
	}
}

export class MainNativeTitlebarPart extends NativeTitlebarPart {
	constructor(
		@IThemeService themeService: IThemeService,
		@IStorageService storageService: IStorageService,
		@IWorkbenchLayoutService layoutService: IWorkbenchLayoutService,
		@IInstantiationService instantiationService: IInstantiationService
	) {
		super(Parts.TITLEBAR_PART, mainWindow, 'main', themeService, storageService, layoutService, instantiationService);
	}
}

export class NativeTitleService extends BrowserTitleService {

	protected override createMainTitlebarPart(): MainNativeTitlebarPart {
		return this.instantiationService.createInstance(MainNativeTitlebarPart);
	}

}
