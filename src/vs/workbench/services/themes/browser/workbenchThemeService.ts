import { Emitter, Event } from '../../../../base/common/event.js';
import { Disposable } from '../../../../base/common/lifecycle.js';
import { InstantiationType, registerSingleton } from '../../../../platform/instantiation/common/extensions.js';
import { IColorTheme, IFileIconTheme, IProductIconTheme } from '../../../../platform/theme/common/themeService.js';
import { ColorThemeData } from '../common/colorThemeData.js';
import { IWorkbenchColorTheme, IWorkbenchThemeService } from '../common/workbenchThemeService.js';

export class WorkbenchThemeService extends Disposable implements IWorkbenchThemeService {
	declare readonly _serviceBrand: undefined;

	private currentColorTheme: ColorThemeData;

	private _onDidColorThemeChange = new Emitter<any>({ leakWarningThreshold: 400 })
	onDidColorThemeChange = this._onDidColorThemeChange.event;

	private _onDidFileIconThemeChange = new Emitter<any>({ leakWarningThreshold: 400 })
	onDidFileIconThemeChange = this._onDidFileIconThemeChange.event;

	private _onDidProductIconThemeChange = new Emitter<any>({ leakWarningThreshold: 400 })
	onDidProductIconThemeChange = this._onDidProductIconThemeChange.event;

	constructor() {
		super();
		this.currentColorTheme = ColorThemeData.createUnloadedTheme('');
	}

	public getColorTheme(): IWorkbenchColorTheme {
		return this.currentColorTheme;
	}


	getFileIconTheme(): IFileIconTheme {
		throw new Error('Method not implemented.');
	}
	getProductIconTheme(): IProductIconTheme {
		throw new Error('Method not implemented.');
	}
}

// The WorkbenchThemeService should stay eager as the constructor restores the
// last used colors / icons from storage. This needs to happen as quickly as possible
// for a flicker-free startup experience.
registerSingleton(IWorkbenchThemeService, WorkbenchThemeService, InstantiationType.Eager);
