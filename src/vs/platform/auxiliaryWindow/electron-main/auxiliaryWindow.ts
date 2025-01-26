import { IBaseWindow } from '../../window/electron-main/window.js';

export interface IAuxiliaryWindow extends IBaseWindow {
	readonly parentId: number;
}
