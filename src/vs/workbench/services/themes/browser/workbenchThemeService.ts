import { createStyleSheet } from '../../../../base/browser/domStylesheets.js';
import { mainWindow } from '../../../../base/browser/window.js';
import { RunOnceScheduler } from '../../../../base/common/async.js';
import { Emitter, Event } from '../../../../base/common/event.js';
import { Disposable, IDisposable } from '../../../../base/common/lifecycle.js';
import { InstantiationType, registerSingleton } from '../../../../platform/instantiation/common/extensions.js';
import { Registry } from '../../../../platform/registry/common/platform.js';
import { IStorageService } from '../../../../platform/storage/common/storage.js';
import { getIconsStyleSheet } from '../../../../platform/theme/browser/iconsStyleSheet.js';
import { asCssVariableName, getColorRegistry } from '../../../../platform/theme/common/colorUtils.js';
import { ColorScheme, ThemeTypeSelector } from '../../../../platform/theme/common/theme.js';
import { IColorTheme, IFileIconTheme, IProductIconTheme, IThemingRegistry, Extensions as ThemingExtensions } from '../../../../platform/theme/common/themeService.js';
import { IBrowserWorkbenchEnvironmentService } from '../../environment/browser/environmentService.js';
import { IWorkbenchLayoutService } from '../../layout/browser/layoutService.js';
import { ColorThemeData } from '../common/colorThemeData.js';
import { ThemeConfiguration } from '../common/themeConfiguration.js';
import { IWorkbenchColorTheme, IWorkbenchThemeService, ThemeSettingDefaults, ThemeSettingTarget } from '../common/workbenchThemeService.js';
import { ProductIconThemeData } from './productIconThemeData.js';

const colorThemeRulesClassName = 'contributedColorTheme';

const themingRegistry = Registry.as<IThemingRegistry>(ThemingExtensions.ThemingContribution);

export class WorkbenchThemeService extends Disposable implements IWorkbenchThemeService {
	declare readonly _serviceBrand: undefined;

	private readonly container: HTMLElement;

	private currentColorTheme: ColorThemeData;
	private currentProductIconTheme: ProductIconThemeData;

	private readonly onColorThemeChange: Emitter<IWorkbenchColorTheme>;
	private colorThemingParticipantChangeListener: IDisposable | undefined;

	private _onDidColorThemeChange = new Emitter<any>({ leakWarningThreshold: 400 })
	onDidColorThemeChange = this._onDidColorThemeChange.event;

	private _onDidFileIconThemeChange = new Emitter<any>({ leakWarningThreshold: 400 })
	onDidFileIconThemeChange = this._onDidFileIconThemeChange.event;

	private _onDidProductIconThemeChange = new Emitter<any>({ leakWarningThreshold: 400 })
	onDidProductIconThemeChange = this._onDidProductIconThemeChange.event;

	constructor(
		@IWorkbenchLayoutService layoutService: IWorkbenchLayoutService,
		@IStorageService private readonly storageService: IStorageService,
		@IBrowserWorkbenchEnvironmentService private readonly environmentService: IBrowserWorkbenchEnvironmentService,
	) {
		super();
		this.container = layoutService.mainContainer;
		this.currentColorTheme = ColorThemeData.createUnloadedTheme('');
		this.currentProductIconTheme = ProductIconThemeData.createUnloadedTheme('');
		this.onColorThemeChange = new Emitter<IWorkbenchColorTheme>({ leakWarningThreshold: 400 });

		this._register(this.onDidColorThemeChange(theme => getColorRegistry().notifyThemeUpdate(theme)));

		// In order to avoid paint flashing for tokens, because
		// themes are loaded asynchronously, we need to initialize
		// a color theme document with good defaults until the theme is loaded
		let themeData: ColorThemeData | undefined = ColorThemeData.fromStorageData(this.storageService);
		const defaultColorMap = undefined;
		themeData = ColorThemeData.createUnloadedThemeForThemeType(ColorScheme.LIGHT, defaultColorMap);

		this.applyTheme(themeData, undefined, true);

		const codiconStyleSheet = createStyleSheet();
		codiconStyleSheet.id = 'codiconStyles';

		const iconsStyleSheet = this._register(getIconsStyleSheet(this));
		function updateAll() {
			codiconStyleSheet.textContent = iconsStyleSheet.getCSS();
		}

		const delayer = this._register(new RunOnceScheduler(updateAll, 0));
		this._register(iconsStyleSheet.onDidChange(() => delayer.schedule()));
		delayer.schedule();
	}

	public getColorTheme(): IWorkbenchColorTheme {
		return this.currentColorTheme;
	}

	private updateDynamicCSSRules(themeData: IColorTheme) {
		const cssRules = new Set<string>();
		const ruleCollector = {
			addRule: (rule: string) => {
				if (!cssRules.has(rule)) {
					cssRules.add(rule);
				}
			}
		};
		ruleCollector.addRule(`.monaco-workbench { forced-color-adjust: none; }`);
		themingRegistry.getThemingParticipants().forEach(p => p(themeData, ruleCollector, this.environmentService))

		const colorVariables: string[] = [];
		for (const item of getColorRegistry().getColors()) {
			const color = themeData.getColor(item.id, true);
			if (color) {
				colorVariables.push(`${asCssVariableName(item.id)}: ${color.toString()};`);
			}
		}
		ruleCollector.addRule(`.monaco-workbench { ${colorVariables.join('\n')} }`);

		_applyRules([...cssRules].join('\n'), colorThemeRulesClassName);
	}

	private applyTheme(newTheme: ColorThemeData, settingsTarget: ThemeSettingTarget, silent = false) {
		this.updateDynamicCSSRules(newTheme);

		if (this.currentColorTheme.id) {
			this.container.classList.remove(...this.currentColorTheme.classNames);
		} else {
			this.container.classList.remove(ThemeTypeSelector.VS, ThemeTypeSelector.VS_DARK, ThemeTypeSelector.HC_BLACK, ThemeTypeSelector.HC_LIGHT);
		}
		this.container.classList.add(...newTheme.classNames);

		this.currentColorTheme.clearCaches();
		this.currentColorTheme = newTheme;

		if (!this.colorThemingParticipantChangeListener) {
			this.colorThemingParticipantChangeListener = themingRegistry.onThemingParticipantAdded(_ => this.updateDynamicCSSRules(this.currentColorTheme));
		}

		if (silent) {
			return Promise.resolve(null);
		}

		this.onColorThemeChange.fire(this.currentColorTheme);

		// remember theme data for a quick restore
		if (newTheme.isLoaded && settingsTarget !== 'preview') {
			newTheme.toStorage(this.storageService);
		}

		return;
		// return this.settings.setColorTheme(this.currentColorTheme, settingsTarget);
	}

	public getFileIconTheme(): IFileIconTheme {
		throw new Error('Method not implemented.');
	}
	public getProductIconTheme() {
		return this.currentProductIconTheme;
	}
}

function _applyRules(styleSheetContent: string, rulesClassName: string) {
	const themeStyles = mainWindow.document.head.getElementsByClassName(rulesClassName);
	if (themeStyles.length === 0) {
		const elStyle = createStyleSheet();
		elStyle.className = rulesClassName;
		elStyle.textContent = styleSheetContent;
	} else {
		(<HTMLStyleElement>themeStyles[0]).textContent = styleSheetContent;
	}
}

// The WorkbenchThemeService should stay eager as the constructor restores the
// last used colors / icons from storage. This needs to happen as quickly as possible
// for a flicker-free startup experience.
registerSingleton(IWorkbenchThemeService, WorkbenchThemeService, InstantiationType.Eager);
