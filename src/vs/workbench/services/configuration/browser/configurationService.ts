import { Emitter, Event } from '../../../../base/common/event.js';
import { Disposable } from '../../../../base/common/lifecycle.js';
import { IConfigurationChangeEvent, IConfigurationData, IConfigurationOverrides, ConfigurationTarget, IConfigurationUpdateOverrides, IConfigurationUpdateOptions, IConfigurationValue } from '../../../../platform/configuration/common/configuration.js';
import { IAnyWorkspaceIdentifier } from '../../../../platform/workspace/common/workspace.js';
import { IWorkbenchConfigurationService, RestrictedSettings } from '../common/configuration.js';

export class WorkbenchConfigurationService extends Disposable implements IWorkbenchConfigurationService {
	_serviceBrand: undefined;

	constructor() {
		super();
	}
	restrictedSettings: RestrictedSettings;
	onDidChangeRestrictedSettings: Event<RestrictedSettings>;
	whenRemoteConfigurationLoaded(): Promise<void> {
		throw new Error('Method not implemented.');
	}
	initialize(arg: IAnyWorkspaceIdentifier): Promise<void> {
		return Promise.resolve();
	}
	isSettingAppliedForAllProfiles(setting: string): boolean {
		throw new Error('Method not implemented.');
	}
	private _onDidChangeConfiguration = new Emitter<IConfigurationChangeEvent>()
	onDidChangeConfiguration = this._onDidChangeConfiguration.event;
	getConfigurationData(): IConfigurationData | null {
		throw new Error('Method not implemented.');
	}
	getValue<T>(): T;
	getValue<T>(section: string): T;
	getValue<T>(overrides: IConfigurationOverrides): T;
	getValue<T>(section: string, overrides: IConfigurationOverrides): T;
	getValue(arg1?: any, arg2?: any): any {
		// TODO: @BABA
		return undefined;
		throw new Error('Method not implemented.');
	}
	updateValue(key: string, value: any): Promise<void>;
	updateValue(key: string, value: any, target: ConfigurationTarget): Promise<void>;
	updateValue(key: string, value: any, overrides: IConfigurationOverrides | IConfigurationUpdateOverrides): Promise<void>;
	updateValue(key: string, value: any, overrides: IConfigurationOverrides | IConfigurationUpdateOverrides, target: ConfigurationTarget, options?: IConfigurationUpdateOptions): Promise<void>;
	updateValue(key: unknown, value: unknown, overrides?: unknown, target?: unknown, options?: unknown): Promise<void> {
		throw new Error('Method not implemented.');
	}
	inspect<T>(key: string, overrides?: IConfigurationOverrides): IConfigurationValue<Readonly<T>> {
		throw new Error('Method not implemented.');
	}
	reloadConfiguration(target?: ConfigurationTarget): Promise<void> {
		throw new Error('Method not implemented.');
	}
	keys(): { default: string[]; user: string[]; workspace: string[]; workspaceFolder: string[]; memory?: string[]; } {
		throw new Error('Method not implemented.');
	}
}
