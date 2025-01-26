/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { VSBuffer } from '../../../base/common/buffer.js';
import { PerformanceMark } from '../../../base/common/performance.js';
import { isMacintosh, isWeb } from '../../../base/common/platform.js';
import { URI, UriComponents, UriDto } from '../../../base/common/uri.js';
import { ISandboxConfiguration } from '../../../base/parts/sandbox/common/sandboxTypes.js';
import { NativeParsedArgs } from '../../environment/common/argv.js';
import { ILoggerResource, LogLevel } from '../../log/common/log.js';

export const WindowMinimumSize = {
	WIDTH: 400,
	WIDTH_WITH_VERTICAL_PANEL: 600,
	HEIGHT: 270
};

export interface IPoint {
	readonly x: number;
	readonly y: number;
}

export interface IRectangle extends IPoint {
	readonly width: number;
	readonly height: number;
}

export interface IBaseOpenWindowsOptions {

	/**
	 * Whether to reuse the window or open a new one.
	 */
	readonly forceReuseWindow?: boolean;

	/**
	 * The remote authority to use when windows are opened with either
	 * - no workspace (empty window)
	 * - a workspace that is neither `file://` nor `vscode-remote://`
	 * Use 'null' for a local window.
	 * If not set, defaults to the remote authority of the current window.
	 */
	readonly remoteAuthority?: string | null;

	readonly forceProfile?: string;
	readonly forceTempProfile?: boolean;
}

export interface IOpenWindowOptions extends IBaseOpenWindowsOptions {
	readonly forceNewWindow?: boolean;
	readonly preferNewWindow?: boolean;

	readonly noRecentEntry?: boolean;

	readonly addMode?: boolean;
	readonly removeMode?: boolean;

	readonly diffMode?: boolean;
	readonly mergeMode?: boolean;
	readonly gotoLineMode?: boolean;

	readonly waitMarkerFileURI?: URI;
}

export interface IAddRemoveFoldersRequest {
	readonly foldersToAdd: UriComponents[];
	readonly foldersToRemove: UriComponents[];
}

interface IOpenedWindow {
	readonly id: number;
	readonly title: string;
	readonly filename?: string;
}

export interface IOpenedMainWindow extends IOpenedWindow {
	readonly dirty: boolean;
}

export interface IOpenedAuxiliaryWindow extends IOpenedWindow {
	readonly parentId: number;
}

export function isOpenedAuxiliaryWindow(candidate: IOpenedMainWindow | IOpenedAuxiliaryWindow): candidate is IOpenedAuxiliaryWindow {
	return typeof (candidate as IOpenedAuxiliaryWindow).parentId === 'number';
}

export interface IOpenEmptyWindowOptions extends IBaseOpenWindowsOptions { }

export type IWindowOpenable = IWorkspaceToOpen | IFolderToOpen | IFileToOpen;

export interface IBaseWindowOpenable {
	label?: string;
}

export interface IWorkspaceToOpen extends IBaseWindowOpenable {
	readonly workspaceUri: URI;
}

export interface IFolderToOpen extends IBaseWindowOpenable {
	readonly folderUri: URI;
}

export interface IFileToOpen extends IBaseWindowOpenable {
	readonly fileUri: URI;
}

export function isWorkspaceToOpen(uriToOpen: IWindowOpenable): uriToOpen is IWorkspaceToOpen {
	return !!(uriToOpen as IWorkspaceToOpen).workspaceUri;
}

export function isFolderToOpen(uriToOpen: IWindowOpenable): uriToOpen is IFolderToOpen {
	return !!(uriToOpen as IFolderToOpen).folderUri;
}

export function isFileToOpen(uriToOpen: IWindowOpenable): uriToOpen is IFileToOpen {
	return !!(uriToOpen as IFileToOpen).fileUri;
}

export type MenuBarVisibility = 'classic' | 'visible' | 'toggle' | 'hidden' | 'compact';

export interface IWindowsConfiguration {
	readonly window: IWindowSettings;
}

export interface IWindowSettings {
	readonly openFilesInNewWindow: 'on' | 'off' | 'default';
	readonly openFoldersInNewWindow: 'on' | 'off' | 'default';
	readonly openWithoutArgumentsInNewWindow: 'on' | 'off';
	readonly restoreWindows: 'preserve' | 'all' | 'folders' | 'one' | 'none';
	readonly restoreFullscreen: boolean;
	readonly zoomLevel: number;
	readonly titleBarStyle: TitlebarStyle;
	readonly autoDetectHighContrast: boolean;
	readonly autoDetectColorScheme: boolean;
	readonly menuBarVisibility: MenuBarVisibility;
	readonly newWindowDimensions: 'default' | 'inherit' | 'offset' | 'maximized' | 'fullscreen';
	readonly nativeTabs: boolean;
	readonly nativeFullScreen: boolean;
	readonly enableMenuBarMnemonics: boolean;
	readonly closeWhenEmpty: boolean;
	readonly clickThroughInactive: boolean;
	readonly newWindowProfile: string;
	readonly density: IDensitySettings;
	readonly experimentalControlOverlay?: boolean;
}

export interface IDensitySettings {
	readonly editorTabHeight: 'default' | 'compact';
}

export const enum TitleBarSetting {
	TITLE_BAR_STYLE = 'window.titleBarStyle',
	CUSTOM_TITLE_BAR_VISIBILITY = 'window.customTitleBarVisibility',
}

export const enum TitlebarStyle {
	NATIVE = 'native',
	CUSTOM = 'custom',
}

export const enum CustomTitleBarVisibility {
	AUTO = 'auto',
	WINDOWED = 'windowed',
	NEVER = 'never',
}

export let titlebarStyleDefaultOverride: 'custom' | undefined = undefined;
export function overrideDefaultTitlebarStyle(style: 'custom'): void {
	titlebarStyleDefaultOverride = style;
}


export const DEFAULT_CUSTOM_TITLEBAR_HEIGHT = 35; // includes space for command center

export function useWindowControlsOverlay(): boolean {
	if (isMacintosh || isWeb) {
		return false; // only supported on a Windows/Linux desktop instances
	}

	// Default to true.
	return true;
}


export interface INativeRunActionInWindowRequest {
	readonly id: string;
	readonly from: 'menu' | 'touchbar' | 'mouse';
	readonly args?: any[];
}

export interface INativeRunKeybindingInWindowRequest {
	readonly userSettingsLabel: string;
}

export interface IColorScheme {
	readonly dark: boolean;
	readonly highContrast: boolean;
}


export interface IOSConfiguration {
	readonly release: string;
	readonly hostname: string;
	readonly arch: string;
}

export interface INativeWindowConfiguration extends NativeParsedArgs, ISandboxConfiguration {
	mainPid: number;
	handle?: VSBuffer;

	execPath: string;

	homeDir: string;
	tmpDir: string;
	userDataDir: string;


	isInitialStartup?: boolean;
	logLevel: LogLevel;
	loggers: {
		global: UriDto<ILoggerResource>[];
		window: UriDto<ILoggerResource>[];
	};

	fullscreen?: boolean;
	maximized?: boolean;
	accessibilitySupport?: boolean;
	colorScheme: IColorScheme;
	autoDetectHighContrast?: boolean;
	autoDetectColorScheme?: boolean;
	isCustomZoomLevel?: boolean;
	overrideDefaultTitlebarStyle?: 'custom';

	perfMarks: PerformanceMark[];

	os: IOSConfiguration;
}

/**
 * According to Electron docs: `scale := 1.2 ^ level`.
 * https://github.com/electron/electron/blob/master/docs/api/web-contents.md#contentssetzoomlevellevel
 */
export function zoomLevelToZoomFactor(zoomLevel = 0): number {
	return Math.pow(1.2, zoomLevel);
}
