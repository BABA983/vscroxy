import { Dimension } from '../../../../base/browser/dom.js';
import { mainWindow } from '../../../../base/browser/window.js';
import { IInstantiationService } from '../../../../platform/instantiation/common/instantiation.js';
import { IStorageService } from '../../../../platform/storage/common/storage.js';
import { IThemeService } from '../../../../platform/theme/common/themeService.js';
import { IWorkbenchLayoutService, Parts } from '../../../services/layout/browser/layoutService.js';
import { Part } from '../../part.js';

export class EditorPart extends Part {
	override minimumWidth: number = 0;
	override maximumWidth: number = Number.POSITIVE_INFINITY;
	override minimumHeight: number = 0;
	override maximumHeight: number = Number.POSITIVE_INFINITY;
	override toJSON(): object {
		return {
			type: Parts.EDITOR_PART
		};
	}

	protected container: HTMLElement | undefined;

	private top = 0;
	private left = 0;
	private _contentDimension!: Dimension;
	get contentDimension(): Dimension { return this._contentDimension; }

	constructor(
		id: string,
		readonly windowId: number,
		@IInstantiationService private readonly instantiationService: IInstantiationService,
		@IThemeService themeService: IThemeService,
		@IStorageService storageService: IStorageService,
		@IWorkbenchLayoutService layoutService: IWorkbenchLayoutService,
	) {
		super(id, { hasTitle: false }, themeService, storageService, layoutService);
	}

	protected override createContentArea(parent: HTMLElement, options?: object): HTMLElement | undefined {
		this.element = parent;
		this.container = document.createElement('div');
		this.container.classList.add('content');

		this.container.textContent = 'Editor Part';

		parent.appendChild(this.container);

		return this.container;
	}

	override layout(width: number, height: number, top: number, left: number): void {
		this.top = top;
		this.left = left;

		super.layoutContents(width, height);
	}

	private doLayout(dimension: Dimension) {
		this._contentDimension = dimension;
	}
}

export class MainEditorPart extends EditorPart {

	constructor(
		@IInstantiationService instantiationService: IInstantiationService,
		@IThemeService themeService: IThemeService,
		@IStorageService storageService: IStorageService,
		@IWorkbenchLayoutService layoutService: IWorkbenchLayoutService,
	) {
		super(Parts.EDITOR_PART, mainWindow.vscodeWindowId, instantiationService, themeService, storageService, layoutService);
	}
}

