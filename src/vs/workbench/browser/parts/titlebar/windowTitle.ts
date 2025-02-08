/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { getWindowById } from '../../../../base/browser/dom.js';
import { CodeWindow } from '../../../../base/browser/window.js';
import { RunOnceScheduler } from '../../../../base/common/async.js';
import { Emitter } from '../../../../base/common/event.js';
import { Disposable, DisposableStore } from '../../../../base/common/lifecycle.js';
import { isMacintosh, isNative, isWeb, isWindows } from '../../../../base/common/platform.js';
import { trim } from '../../../../base/common/strings.js';
import { localize } from '../../../../nls.js';
import { IConfigurationChangeEvent } from '../../../../platform/configuration/common/configuration.js';
import { IProductService } from '../../../../platform/product/common/productService.js';
import { IEditorGroupsContainer } from '../../../services/editor/common/editorGroupsService.js';
import { IBrowserWorkbenchEnvironmentService } from '../../../services/environment/browser/environmentService.js';

const enum WindowSettingNames {
	titleSeparator = 'window.titleSeparator',
	title = 'window.title'
}

export const defaultWindowTitle = (() => {
	if (isMacintosh && isNative) {
		return '${activeEditorShort}${separator}${rootName}${separator}${profileName}'; // macOS has native dirty indicator
	}

	const base = '${dirty}${activeEditorShort}${separator}${rootName}${separator}${profileName}${separator}${appName}';
	if (isWeb) {
		return base + '${separator}${remoteName}'; // Web: always show remote name
	}

	return base;
})();
export const defaultWindowTitleSeparator = isMacintosh ? ' \u2014 ' : ' - ';

export class WindowTitle extends Disposable {

	private static readonly NLS_USER_IS_ADMIN = isWindows ? localize('userIsAdmin', "[Administrator]") : localize('userIsSudo', "[Superuser]");
	private static readonly NLS_EXTENSION_HOST = localize('devExtensionWindowTitlePrefix', "[Extension Development Host]");
	private static readonly TITLE_DIRTY = '\u25cf ';

	private readonly properties: any = { isPure: true, isAdmin: false, prefix: undefined };
	private readonly variables = new Map<string /* context key */, string /* name */>();

	private readonly activeEditorListeners = this._register(new DisposableStore());
	private readonly titleUpdater = this._register(new RunOnceScheduler(() => this.doUpdateTitle(), 0));

	private readonly onDidChangeEmitter = new Emitter<void>();
	readonly onDidChange = this.onDidChangeEmitter.event;

	get value() { return this.title ?? ''; }
	get workspaceName() { return 'Listening on 127.0.0.1:7890'; }
	get fileName() {
		// const activeEditor = this.editorService.activeEditor;
		// if (!activeEditor) {
		// 	return undefined;
		// }
		// const fileName = activeEditor.getTitle(Verbosity.SHORT);
		// const dirty = activeEditor?.isDirty() && !activeEditor.isSaving() ? WindowTitle.TITLE_DIRTY : '';
		return `TODO: filename`;
	}

	private title: string | undefined;
	private titleIncludesFocusedView: boolean = false;

	// private readonly editorService: IEditorService;

	private readonly windowId: number;

	constructor(
		targetWindow: CodeWindow,
		editorGroupsContainer: IEditorGroupsContainer | 'main',
		@IBrowserWorkbenchEnvironmentService protected readonly environmentService: IBrowserWorkbenchEnvironmentService,
		@IProductService private readonly productService: IProductService,
	) {
		super();

		this.windowId = targetWindow.vscodeWindowId;

		this.updateTitleIncludesFocusedView();
		this.registerListeners();
	}

	private registerListeners(): void {

	}

	private onConfigurationChanged(event: IConfigurationChangeEvent): void {
		if (event.affectsConfiguration(WindowSettingNames.title)) {
			this.updateTitleIncludesFocusedView();
		}

		if (event.affectsConfiguration(WindowSettingNames.title) || event.affectsConfiguration(WindowSettingNames.titleSeparator)) {
			this.titleUpdater.schedule();
		}
	}

	private updateTitleIncludesFocusedView(): void {
		// const titleTemplate = this.configurationService.getValue<unknown>(WindowSettingNames.title);
		// this.titleIncludesFocusedView = typeof titleTemplate === 'string' && titleTemplate.includes('${focusedView}');
	}

	private onActiveEditorChange(): void {

		// // Dispose old listeners
		// this.activeEditorListeners.clear();

		// // Calculate New Window Title
		// this.titleUpdater.schedule();

		// // Apply listener for dirty and label changes
		// const activeEditor = this.editorService.activeEditor;
		// if (activeEditor) {
		// 	this.activeEditorListeners.add(activeEditor.onDidChangeDirty(() => this.titleUpdater.schedule()));
		// 	this.activeEditorListeners.add(activeEditor.onDidChangeLabel(() => this.titleUpdater.schedule()));
		// }

		// // Apply listeners for tracking focused code editor
		// if (this.titleIncludesFocusedView) {
		// 	const activeTextEditorControl = this.editorService.activeTextEditorControl;
		// 	const textEditorControls: ICodeEditor[] = [];
		// 	if (isCodeEditor(activeTextEditorControl)) {
		// 		textEditorControls.push(activeTextEditorControl);
		// 	} else if (isDiffEditor(activeTextEditorControl)) {
		// 		textEditorControls.push(activeTextEditorControl.getOriginalEditor(), activeTextEditorControl.getModifiedEditor());
		// 	}

		// 	for (const textEditorControl of textEditorControls) {
		// 		this.activeEditorListeners.add(textEditorControl.onDidBlurEditorText(() => this.titleUpdater.schedule()));
		// 		this.activeEditorListeners.add(textEditorControl.onDidFocusEditorText(() => this.titleUpdater.schedule()));
		// 	}
		// }
	}

	private doUpdateTitle(): void {
		const title = this.getFullWindowTitle();
		if (title !== this.title) {

			// Always set the native window title to identify us properly to the OS
			let nativeTitle = title;
			if (!trim(nativeTitle)) {
				nativeTitle = this.productService.nameLong;
			}

			const window = getWindowById(this.windowId, true).window;
			if (!window.document.title && isMacintosh && nativeTitle === this.productService.nameLong) {
				// TODO@electron macOS: if we set a window title for
				// the first time and it matches the one we set in
				// `windowImpl.ts` somehow the window does not appear
				// in the "Windows" menu. As such, we set the title
				// briefly to something different to ensure macOS
				// recognizes we have a window.
				// See: https://github.com/microsoft/vscode/issues/191288
				window.document.title = `${this.productService.nameLong} ${WindowTitle.TITLE_DIRTY}`;
			}

			window.document.title = nativeTitle;
			this.title = title;

			this.onDidChangeEmitter.fire();
		}
	}

	private getFullWindowTitle(): string {
		const { prefix, suffix } = this.getTitleDecorations();

		let title = this.getWindowTitle() || this.productService.nameLong;
		if (prefix) {
			title = `${prefix} ${title}`;
		}

		if (suffix) {
			title = `${title} ${suffix}`;
		}

		// Replace non-space whitespace
		return title.replace(/[^\S ]/g, ' ');
	}

	getTitleDecorations() {
		let prefix: string | undefined;
		let suffix: string | undefined;

		if (this.properties.prefix) {
			prefix = this.properties.prefix;
		}

		if (this.environmentService.isExtensionDevelopment) {
			prefix = !prefix
				? WindowTitle.NLS_EXTENSION_HOST
				: `${WindowTitle.NLS_EXTENSION_HOST} - ${prefix}`;
		}

		if (this.properties.isAdmin) {
			suffix = WindowTitle.NLS_USER_IS_ADMIN;
		}

		return { prefix, suffix };
	}

	updateProperties(properties: any): void {
		// const isAdmin = typeof properties.isAdmin === 'boolean' ? properties.isAdmin : this.properties.isAdmin;
		// const isPure = typeof properties.isPure === 'boolean' ? properties.isPure : this.properties.isPure;
		// const prefix = typeof properties.prefix === 'string' ? properties.prefix : this.properties.prefix;

		// if (isAdmin !== this.properties.isAdmin || isPure !== this.properties.isPure || prefix !== this.properties.prefix) {
		// 	this.properties.isAdmin = isAdmin;
		// 	this.properties.isPure = isPure;
		// 	this.properties.prefix = prefix;

		// 	this.titleUpdater.schedule();
		// }
	}

	registerVariables(variables: any[]): void {
		let changed = false;

		for (const { name, contextKey } of variables) {
			if (!this.variables.has(contextKey)) {
				this.variables.set(contextKey, name);

				changed = true;
			}
		}

		if (changed) {
			this.titleUpdater.schedule();
		}
	}

	/**
	 * Possible template values:
	 *
	 * {activeEditorLong}: e.g. /Users/Development/myFolder/myFileFolder/myFile.txt
	 * {activeEditorMedium}: e.g. myFolder/myFileFolder/myFile.txt
	 * {activeEditorShort}: e.g. myFile.txt
	 * {activeFolderLong}: e.g. /Users/Development/myFolder/myFileFolder
	 * {activeFolderMedium}: e.g. myFolder/myFileFolder
	 * {activeFolderShort}: e.g. myFileFolder
	 * {rootName}: e.g. myFolder1, myFolder2, myFolder3
	 * {rootPath}: e.g. /Users/Development
	 * {folderName}: e.g. myFolder
	 * {folderPath}: e.g. /Users/Development/myFolder
	 * {appName}: e.g. VS Code
	 * {remoteName}: e.g. SSH
	 * {dirty}: indicator
	 * {focusedView}: e.g. Terminal
	 * {separator}: conditional separator
	 */
	getWindowTitle(): string {


		return 'Hello VSCroxy'
	}

	isCustomTitleFormat(): boolean {

		return false;
	}
}
