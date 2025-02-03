import { isFullscreen, isWCOEnabled } from '../../base/browser/browser.js';
import { getActiveDocument, getClientArea, getWindow, IDimension, isActiveDocument, position, size } from '../../base/browser/dom.js';
import { Direction, ISerializableView, ISerializedGrid, ISerializedLeafNode, ISerializedNode, IViewSize, Orientation, SerializableGrid } from '../../base/browser/ui/grid/grid.js';
import { mainWindow } from '../../base/browser/window.js';
import { coalesce } from '../../base/common/arrays.js';
import { DeferredPromise, Promises } from '../../base/common/async.js';
import { Emitter } from '../../base/common/event.js';
import { Disposable, DisposableMap, DisposableStore, IDisposable, toDisposable } from '../../base/common/lifecycle.js';
import { mark } from '../../base/common/performance.js';
import { isWeb, isWindows } from '../../base/common/platform.js';
import { IFileService } from '../../platform/files/common/files.js';
import { ServicesAccessor } from '../../platform/instantiation/common/instantiation.js';
import { ILogService } from '../../platform/log/common/log.js';
import { StorageScope, StorageTarget } from '../../platform/storage/common/storage.js';
import { useWindowControlsOverlay } from '../../platform/window/common/window.js';
import { IViewDescriptorService } from '../common/views.js';
import { IBrowserWorkbenchEnvironmentService } from '../services/environment/browser/environmentService.js';
import { isHorizontal, IWorkbenchLayoutService, MULTI_WINDOW_PARTS, PanelAlignment, Parts, Position, positionFromString, SINGLE_WINDOW_PARTS } from '../services/layout/browser/layoutService.js';
import { ILifecycleService } from '../services/lifecycle/common/lifecycle.js';
import { IPaneCompositePartService } from '../services/panecomposite/browser/panecomposite.js';
import { IStatusbarService } from '../services/statusbar/browser/statusbar.js';
import { ITitleService } from '../services/title/browser/titleService.js';
import { Part } from './part.js';

enum LayoutClasses {
	SIDEBAR_HIDDEN = 'nosidebar',
	MAIN_EDITOR_AREA_HIDDEN = 'nomaineditorarea',
	PANEL_HIDDEN = 'nopanel',
	AUXILIARYBAR_HIDDEN = 'noauxiliarybar',
	STATUSBAR_HIDDEN = 'nostatusbar',
	FULLSCREEN = 'fullscreen',
	MAXIMIZED = 'maximized',
	WINDOW_BORDER = 'border'
}


interface ILayoutRuntimeState {
	activeContainerId: number;
	mainWindowFullscreen: boolean;
	readonly maximized: Set<number>;
	hasFocus: boolean;
	mainWindowBorder: boolean;
	readonly menuBar: {
		toggled: boolean;
	};
	readonly zenMode: {
		readonly transitionDisposables: DisposableMap<string, IDisposable>;
	};
}

interface ILayoutInitializationState {
	readonly views: {
		readonly defaults: string[] | undefined;
		readonly containerToRestore: {
			sideBar?: string;
			panel?: string;
			auxiliaryBar?: string;
		};
	};
}

interface ILayoutState {
	readonly runtime: ILayoutRuntimeState;
	readonly initialization: ILayoutInitializationState;
}

interface IInitialEditorsState {

}

export abstract class Layout extends Disposable implements IWorkbenchLayoutService {

	declare readonly _serviceBrand: undefined;

	//#region Events

	private readonly _onDidChangeZenMode = this._register(new Emitter<boolean>());
	readonly onDidChangeZenMode = this._onDidChangeZenMode.event;

	private readonly _onDidChangeMainEditorCenteredLayout = this._register(new Emitter<boolean>());
	readonly onDidChangeMainEditorCenteredLayout = this._onDidChangeMainEditorCenteredLayout.event;

	private readonly _onDidChangePanelAlignment = this._register(new Emitter<PanelAlignment>());
	readonly onDidChangePanelAlignment = this._onDidChangePanelAlignment.event;

	private readonly _onDidChangeWindowMaximized = this._register(new Emitter<{ windowId: number; maximized: boolean }>());
	readonly onDidChangeWindowMaximized = this._onDidChangeWindowMaximized.event;

	private readonly _onDidChangePanelPosition = this._register(new Emitter<string>());
	readonly onDidChangePanelPosition = this._onDidChangePanelPosition.event;

	private readonly _onDidChangePartVisibility = this._register(new Emitter<void>());
	readonly onDidChangePartVisibility = this._onDidChangePartVisibility.event;

	private readonly _onDidChangeNotificationsVisibility = this._register(new Emitter<boolean>());
	readonly onDidChangeNotificationsVisibility = this._onDidChangeNotificationsVisibility.event;

	private readonly _onDidLayoutMainContainer = this._register(new Emitter<IDimension>());
	readonly onDidLayoutMainContainer = this._onDidLayoutMainContainer.event;

	private readonly _onDidLayoutActiveContainer = this._register(new Emitter<IDimension>());
	readonly onDidLayoutActiveContainer = this._onDidLayoutActiveContainer.event;

	private readonly _onDidLayoutContainer = this._register(new Emitter<{ container: HTMLElement; dimension: IDimension }>());
	readonly onDidLayoutContainer = this._onDidLayoutContainer.event;

	private readonly _onDidAddContainer = this._register(new Emitter<{ container: HTMLElement; disposables: DisposableStore }>());
	readonly onDidAddContainer = this._onDidAddContainer.event;

	private readonly _onDidChangeActiveContainer = this._register(new Emitter<void>());
	readonly onDidChangeActiveContainer = this._onDidChangeActiveContainer.event;

	//#endregion

	private logService!: ILogService;
	private environmentService!: IBrowserWorkbenchEnvironmentService;

	// Parts
	private titleService!: ITitleService;
	private statusBarService!: ITitleService;
	private paneCompositeService!: IPaneCompositePartService;
	private viewDescriptorService!: IViewDescriptorService;

	private state!: ILayoutState;
	private stateModel!: LayoutStateModel;

	private disposed = false;


	private readonly parts = new Map<string, Part>();

	private initialized = false;
	private workbenchGrid!: SerializableGrid<ISerializableView>;

	private titleBarPartView!: ISerializableView;
	private bannerPartView!: ISerializableView;
	private activityBarPartView!: ISerializableView;
	private sideBarPartView!: ISerializableView;
	private panelPartView!: ISerializableView;
	private auxiliaryBarPartView!: ISerializableView;
	private editorPartView!: ISerializableView;
	private statusBarPartView!: ISerializableView;

	readonly mainContainer = document.createElement('div');
	get activeContainer() { return this.getContainerFromDocument(getActiveDocument()); }

	private getContainerFromDocument(targetDocument: Document): HTMLElement {
		if (targetDocument === this.mainContainer.ownerDocument) {
			// main window
			return this.mainContainer;
		} else {
			// auxiliary window
			return targetDocument.body.getElementsByClassName('monaco-workbench')[0] as HTMLElement;
		}
	}

	private _mainContainerDimension!: IDimension;
	get mainContainerDimension(): IDimension { return this._mainContainerDimension; }

	constructor(
		protected readonly parent: HTMLElement
	) {
		super();
	}
	openedDefaultEditors: boolean;
	hasFocus(part: Parts): boolean {
		throw new Error('Method not implemented.');
	}
	focusPart(part: SINGLE_WINDOW_PARTS): void;
	focusPart(part: MULTI_WINDOW_PARTS, targetWindow: Window): void;
	focusPart(part: Parts, targetWindow: Window): void;
	focusPart(part: unknown, targetWindow?: unknown): void {
		throw new Error('Method not implemented.');
	}
	getContainer(targetWindow: Window): HTMLElement;
	getContainer(targetWindow: Window, part: Parts): HTMLElement | undefined;
	getContainer(targetWindow: unknown, part?: unknown): HTMLElement | undefined {
		throw new Error('Method not implemented.');
	}
	setPartHidden(hidden: boolean, part: Exclude<SINGLE_WINDOW_PARTS, Parts.STATUSBAR_PART | Parts.TITLEBAR_PART>): void;
	setPartHidden(hidden: boolean, part: Exclude<MULTI_WINDOW_PARTS, Parts.STATUSBAR_PART | Parts.TITLEBAR_PART>, targetWindow: Window): void;
	setPartHidden(hidden: boolean, part: Exclude<Parts, Parts.STATUSBAR_PART | Parts.TITLEBAR_PART>, targetWindow: Window): void;
	setPartHidden(hidden: unknown, part: unknown, targetWindow?: unknown): void {
		throw new Error('Method not implemented.');
	}
	toggleMaximizedPanel(): void {
		throw new Error('Method not implemented.');
	}
	hasMainWindowBorder(): boolean {
		throw new Error('Method not implemented.');
	}
	getMainWindowBorderRadius(): string | undefined {
		throw new Error('Method not implemented.');
	}
	isPanelMaximized(): boolean {
		throw new Error('Method not implemented.');
	}
	getSideBarPosition(): Position {
		return this.stateModel.getRuntimeValue(LayoutStateKeys.SIDEBAR_POSITON);
	}
	toggleMenuBar(): void {
		throw new Error('Method not implemented.');
	}
	getPanelPosition(): Position {
		return this.stateModel.getRuntimeValue(LayoutStateKeys.PANEL_POSITION);
	}
	setPanelPosition(position: Position): void {
		throw new Error('Method not implemented.');
	}
	getPanelAlignment(): PanelAlignment {
		throw new Error('Method not implemented.');
	}
	setPanelAlignment(alignment: PanelAlignment): void {
		throw new Error('Method not implemented.');
	}
	getMaximumEditorDimensions(container: HTMLElement): IDimension {
		throw new Error('Method not implemented.');
	}
	toggleZenMode(): void {
		throw new Error('Method not implemented.');
	}
	isMainEditorLayoutCentered(): boolean {
		throw new Error('Method not implemented.');
	}
	centerMainEditorLayout(active: boolean): void {
		throw new Error('Method not implemented.');
	}
	getSize(part: Parts): IViewSize {
		throw new Error('Method not implemented.');
	}
	setSize(part: Parts, size: IViewSize): void {
		throw new Error('Method not implemented.');
	}
	resizePart(part: Parts, sizeChangeWidth: number, sizeChangeHeight: number): void {
		throw new Error('Method not implemented.');
	}
	isWindowMaximized(targetWindow: Window): boolean {
		throw new Error('Method not implemented.');
	}
	updateWindowMaximizedState(targetWindow: Window, maximized: boolean): void {
		throw new Error('Method not implemented.');
	}
	getVisibleNeighborPart(part: Parts, direction: Direction): Parts | undefined {
		throw new Error('Method not implemented.');
	}

	protected initLayout(accessor: ServicesAccessor) {
		this.logService = accessor.get(ILogService);
		this.environmentService = accessor.get(IBrowserWorkbenchEnvironmentService);

		// Parts
		this.titleService = accessor.get(ITitleService);
		this.statusBarService = accessor.get(IStatusbarService);
		this.paneCompositeService = accessor.get(IPaneCompositePartService);
		this.viewDescriptorService = accessor.get(IViewDescriptorService);

		// Listeners
		this.registerLayoutListeners();

		// State
		this.initLayoutState(accessor.get(ILifecycleService));
	}

	private registerLayoutListeners(): void {

	}

	private initLayoutState(lifecycleService: ILifecycleService): void {
		this.stateModel = new LayoutStateModel(this.parent);
		this.stateModel.load();

		// Layout Initialization State
		const initialEditorsState = this.getInitialEditorsState();

		const initialLayoutState: ILayoutInitializationState = {
			views: {
				defaults: this.getDefaultLayoutViews(this.environmentService),
				containerToRestore: {}
			}
		};

		// Layout Runtime State
		const layoutRuntimeState: ILayoutRuntimeState = {
			activeContainerId: this.getActiveContainerId(),
			mainWindowFullscreen: isFullscreen(mainWindow),
			// TODO: hostService
			hasFocus: false,
			maximized: new Set<number>(),
			mainWindowBorder: false,
			menuBar: {
				toggled: false,
			},
			zenMode: {
				transitionDisposables: new DisposableMap(),
			}
		};

		this.state = {
			initialization: initialLayoutState,
			runtime: layoutRuntimeState,
		};

		// Window border
		this.updateWindowsBorder(true);
	}

	private getDefaultLayoutViews(environmentService: IBrowserWorkbenchEnvironmentService): string[] | undefined {
		const defaultLayout = environmentService.options?.defaultLayout;
		if (!defaultLayout) {
			return undefined;
		}

		const { views } = defaultLayout;
		if (views?.length) {
			return views.map(view => view.id);
		}

		return undefined;
	}


	private updateWindowsBorder(skipLayout = false) {
		// if (
		// 	isWeb ||
		// 	isWindows || 											// not working well with zooming (border often not visible)
		// 	useWindowControlsOverlay() 	// not working with WCO (border cannot draw over the overlay)
		// ) {
		// 	return;
		// }

		// const theme = this.themeService.getColorTheme();

		// const activeBorder = theme.getColor(WINDOW_ACTIVE_BORDER);
		// const inactiveBorder = theme.getColor(WINDOW_INACTIVE_BORDER);

		// const didHaveMainWindowBorder = this.hasMainWindowBorder();

		// for (const container of this.containers) {
		// 	const isMainContainer = container === this.mainContainer;
		// 	const isActiveContainer = this.activeContainer === container;
		// 	const containerWindowId = getWindowId(getWindow(container));

		// 	let windowBorder = false;
		// 	if (!this.state.runtime.mainWindowFullscreen && !this.state.runtime.maximized.has(containerWindowId) && (activeBorder || inactiveBorder)) {
		// 		windowBorder = true;

		// 		// If the inactive color is missing, fallback to the active one
		// 		const borderColor = isActiveContainer && this.state.runtime.hasFocus ? activeBorder : inactiveBorder ?? activeBorder;
		// 		container.style.setProperty('--window-border-color', borderColor?.toString() ?? 'transparent');
		// 	}

		// 	if (isMainContainer) {
		// 		this.state.runtime.mainWindowBorder = windowBorder;
		// 	}

		// 	container.classList.toggle(LayoutClasses.WINDOW_BORDER, windowBorder);
		// }

		// if (!skipLayout && didHaveMainWindowBorder !== this.hasMainWindowBorder()) {
		// 	this.layout();
		// }
	}

	private getActiveContainerId(): number {
		const activeContainer = this.activeContainer;

		return getWindow(activeContainer).vscodeWindowId;
	}

	private getInitialEditorsState(): IInitialEditorsState | undefined {

		return undefined;
	}

	isVisible(part: Parts, targetWindow?: Window): boolean;
	isVisible(part: Parts, targetWindow: Window = mainWindow): boolean {
		return true;
	}

	getLayoutClasses(): string[] {
		return coalesce([
			!this.isVisible(Parts.SIDEBAR_PART) ? LayoutClasses.SIDEBAR_HIDDEN : undefined,
			!this.isVisible(Parts.EDITOR_PART, mainWindow) ? LayoutClasses.MAIN_EDITOR_AREA_HIDDEN : undefined,
			!this.isVisible(Parts.PANEL_PART) ? LayoutClasses.PANEL_HIDDEN : undefined,
			!this.isVisible(Parts.AUXILIARYBAR_PART) ? LayoutClasses.AUXILIARYBAR_HIDDEN : undefined,
			!this.isVisible(Parts.STATUSBAR_PART) ? LayoutClasses.STATUSBAR_HIDDEN : undefined,
			this.state.runtime.mainWindowFullscreen ? LayoutClasses.FULLSCREEN : undefined
		]);
	}

	registerPart(part: Part): IDisposable {
		const id = part.getId();
		this.parts.set(id, part);

		return toDisposable(() => this.parts.delete(id));
	}

	protected getPart(key: Parts): Part {
		const part = this.parts.get(key);
		if (!part) {
			throw new Error(`Unknown part ${key}`);
		}

		return part;
	}

	protected createWorkbenchLayout(): void {
		const titleBar = this.getPart(Parts.TITLEBAR_PART);
		// const bannerPart = this.getPart(Parts.BANNER_PART);
		// const editorPart = this.getPart(Parts.EDITOR_PART);
		// const activityBar = this.getPart(Parts.ACTIVITYBAR_PART);
		const panelPart = this.getPart(Parts.PANEL_PART);
		// const auxiliaryBarPart = this.getPart(Parts.AUXILIARYBAR_PART);
		const sideBar = this.getPart(Parts.SIDEBAR_PART);
		const statusBar = this.getPart(Parts.STATUSBAR_PART);

		// View references for all parts
		this.titleBarPartView = titleBar;
		this.bannerPartView = statusBar;
		this.sideBarPartView = sideBar;
		this.activityBarPartView = statusBar;
		this.editorPartView = statusBar;
		this.panelPartView = panelPart;
		this.auxiliaryBarPartView = statusBar;
		this.statusBarPartView = statusBar;

		const viewMap = {
			[Parts.ACTIVITYBAR_PART]: this.activityBarPartView,
			[Parts.BANNER_PART]: this.bannerPartView,
			[Parts.TITLEBAR_PART]: this.titleBarPartView,
			[Parts.EDITOR_PART]: this.editorPartView,
			[Parts.PANEL_PART]: this.panelPartView,
			[Parts.SIDEBAR_PART]: this.sideBarPartView,
			[Parts.STATUSBAR_PART]: this.statusBarPartView,
			[Parts.AUXILIARYBAR_PART]: this.auxiliaryBarPartView
		};

		const fromJSON = ({ type }: { type: Parts }) => viewMap[type];

		const workbenchGrid = SerializableGrid.deserialize(
			this.createGridDescriptor(),
			{ fromJSON },
			{ proportionalLayout: false }
		);

		this.mainContainer.prepend(workbenchGrid.element);
		this.mainContainer.setAttribute('role', 'application');
		this.workbenchGrid = workbenchGrid;
		this.workbenchGrid.edgeSnapping = this.state.runtime.mainWindowFullscreen;

	}

	layout(): void {
		if (!this.disposed) {
			this._mainContainerDimension = getClientArea(this.state.runtime.mainWindowFullscreen ?
				mainWindow.document.body : 	// in fullscreen mode, make sure to use <body> element because
				this.parent					// in that case the workbench will span the entire site
			);
			this.logService.trace(`Layout#layout, height: ${this._mainContainerDimension.height}, width: ${this._mainContainerDimension.width}`);

			position(this.mainContainer, 0, 0, 0, 0, 'relative');
			size(this.mainContainer, this._mainContainerDimension.width, this._mainContainerDimension.height);

			// Layout the grid widget
			this.workbenchGrid.layout(this._mainContainerDimension.width, this._mainContainerDimension.height);
			this.initialized = true;

			// Emit as event
			this.handleContainerDidLayout(this.mainContainer, this._mainContainerDimension);
		}
	}

	private handleContainerDidLayout(container: HTMLElement, dimension: IDimension): void {
		if (container === this.mainContainer) {
			this._onDidLayoutMainContainer.fire(dimension);
		}

		if (isActiveDocument(container)) {
			this._onDidLayoutActiveContainer.fire(dimension);
		}

		this._onDidLayoutContainer.fire({ container, dimension });
	}

	private createGridDescriptor(): ISerializedGrid {
		const { width, height } = this.stateModel.getInitializationValue(LayoutStateKeys.GRID_SIZE);
		const sideBarSize = this.stateModel.getInitializationValue(LayoutStateKeys.SIDEBAR_SIZE);
		const auxiliaryBarPartSize = this.stateModel.getInitializationValue(LayoutStateKeys.AUXILIARYBAR_SIZE);
		const panelSize = this.stateModel.getInitializationValue(LayoutStateKeys.PANEL_SIZE);

		const titleBarHeight = this.titleBarPartView.minimumHeight;
		const bannerHeight = this.bannerPartView.minimumHeight;
		const statusBarHeight = this.statusBarPartView.minimumHeight;
		const activityBarWidth = this.activityBarPartView.minimumWidth;
		const middleSectionHeight = height - titleBarHeight - statusBarHeight;

		const titleAndBanner: ISerializedNode[] = [
			{
				type: 'leaf',
				data: { type: Parts.TITLEBAR_PART },
				size: titleBarHeight,
				visible: this.isVisible(Parts.TITLEBAR_PART, mainWindow)
			},
			{
				type: 'leaf',
				data: { type: Parts.BANNER_PART },
				size: bannerHeight,
				visible: false
			}
		];

		const activityBarNode: ISerializedLeafNode = {
			type: 'leaf',
			data: { type: Parts.ACTIVITYBAR_PART },
			size: activityBarWidth,
			visible: !this.stateModel.getRuntimeValue(LayoutStateKeys.ACTIVITYBAR_HIDDEN)
		};

		const sideBarNode: ISerializedLeafNode = {
			type: 'leaf',
			data: { type: Parts.SIDEBAR_PART },
			size: sideBarSize,
			visible: !this.stateModel.getRuntimeValue(LayoutStateKeys.SIDEBAR_HIDDEN)
		};

		const auxiliaryBarNode: ISerializedLeafNode = {
			type: 'leaf',
			data: { type: Parts.AUXILIARYBAR_PART },
			size: auxiliaryBarPartSize,
			visible: this.isVisible(Parts.AUXILIARYBAR_PART)
		};

		const editorNode: ISerializedLeafNode = {
			type: 'leaf',
			data: { type: Parts.EDITOR_PART },
			size: 0, // Update based on sibling sizes
			visible: !this.stateModel.getRuntimeValue(LayoutStateKeys.EDITOR_HIDDEN)
		};

		const panelNode: ISerializedLeafNode = {
			type: 'leaf',
			data: { type: Parts.PANEL_PART },
			size: panelSize,
			visible: !this.stateModel.getRuntimeValue(LayoutStateKeys.PANEL_HIDDEN)
		};

		const middleSection: ISerializedNode[] = this.arrangeMiddleSectionNodes({
			activityBar: activityBarNode,
			auxiliaryBar: auxiliaryBarNode,
			editor: editorNode,
			panel: panelNode,
			sideBar: sideBarNode
		}, width, middleSectionHeight);

		const result: ISerializedGrid = {
			root: {
				type: 'branch',
				size: width,
				data: [
					...(this.shouldShowBannerFirst() ? titleAndBanner.reverse() : titleAndBanner),
					{
						type: 'branch',
						data: middleSection,
						size: middleSectionHeight
					},
					{
						type: 'leaf',
						data: { type: Parts.STATUSBAR_PART },
						size: statusBarHeight,
						visible: !this.stateModel.getRuntimeValue(LayoutStateKeys.STATUSBAR_HIDDEN)
					}
				]
			},
			orientation: Orientation.VERTICAL,
			width,
			height
		};

		return result;
	}

	private shouldShowBannerFirst(): boolean {
		return isWeb && !isWCOEnabled();
	}

	private arrangeEditorNodes(nodes: { editor: ISerializedNode; sideBar?: ISerializedNode; auxiliaryBar?: ISerializedNode }, availableHeight: number, availableWidth: number): ISerializedNode {
		if (!nodes.sideBar && !nodes.auxiliaryBar) {
			nodes.editor.size = availableHeight;
			return nodes.editor;
		}

		const result = [nodes.editor];
		nodes.editor.size = availableWidth;
		if (nodes.sideBar) {
			if (this.stateModel.getRuntimeValue(LayoutStateKeys.SIDEBAR_POSITON) === Position.LEFT) {
				result.splice(0, 0, nodes.sideBar);
			} else {
				result.push(nodes.sideBar);
			}

			nodes.editor.size -= this.stateModel.getRuntimeValue(LayoutStateKeys.SIDEBAR_HIDDEN) ? 0 : nodes.sideBar.size;
		}

		if (nodes.auxiliaryBar) {
			if (this.stateModel.getRuntimeValue(LayoutStateKeys.SIDEBAR_POSITON) === Position.RIGHT) {
				result.splice(0, 0, nodes.auxiliaryBar);
			} else {
				result.push(nodes.auxiliaryBar);
			}

			nodes.editor.size -= this.stateModel.getRuntimeValue(LayoutStateKeys.AUXILIARYBAR_HIDDEN) ? 0 : nodes.auxiliaryBar.size;
		}

		return {
			type: 'branch',
			data: result,
			size: availableHeight
		};
	}

	private arrangeMiddleSectionNodes(nodes: { editor: ISerializedNode; panel: ISerializedNode; activityBar: ISerializedNode; sideBar: ISerializedNode; auxiliaryBar: ISerializedNode }, availableWidth: number, availableHeight: number): ISerializedNode[] {
		const activityBarSize = this.stateModel.getRuntimeValue(LayoutStateKeys.ACTIVITYBAR_HIDDEN) ? 0 : nodes.activityBar.size;
		const sideBarSize = this.stateModel.getRuntimeValue(LayoutStateKeys.SIDEBAR_HIDDEN) ? 0 : nodes.sideBar.size;
		const auxiliaryBarSize = this.stateModel.getRuntimeValue(LayoutStateKeys.AUXILIARYBAR_HIDDEN) ? 0 : nodes.auxiliaryBar.size;
		const panelSize = this.stateModel.getInitializationValue(LayoutStateKeys.PANEL_SIZE) ? 0 : nodes.panel.size;

		const panelPostion = this.stateModel.getRuntimeValue(LayoutStateKeys.PANEL_POSITION);
		const sideBarPosition = this.stateModel.getRuntimeValue(LayoutStateKeys.SIDEBAR_POSITON);

		const result = [] as ISerializedNode[];
		if (!isHorizontal(panelPostion)) {
			result.push(nodes.editor);
			nodes.editor.size = availableWidth - activityBarSize - sideBarSize - panelSize - auxiliaryBarSize;
			if (panelPostion === Position.RIGHT) {
				result.push(nodes.panel);
			} else {
				result.splice(0, 0, nodes.panel);
			}

			if (sideBarPosition === Position.LEFT) {
				result.push(nodes.auxiliaryBar);
				result.splice(0, 0, nodes.sideBar);
				result.splice(0, 0, nodes.activityBar);
			} else {
				result.splice(0, 0, nodes.auxiliaryBar);
				result.push(nodes.sideBar);
				result.push(nodes.activityBar);
			}
		} else {
			const panelAlignment = this.stateModel.getRuntimeValue(LayoutStateKeys.PANEL_ALIGNMENT);
			const sideBarNextToEditor = !(panelAlignment === 'center' || (sideBarPosition === Position.LEFT && panelAlignment === 'right') || (sideBarPosition === Position.RIGHT && panelAlignment === 'left'));
			const auxiliaryBarNextToEditor = !(panelAlignment === 'center' || (sideBarPosition === Position.RIGHT && panelAlignment === 'right') || (sideBarPosition === Position.LEFT && panelAlignment === 'left'));

			const editorSectionWidth = availableWidth - activityBarSize - (sideBarNextToEditor ? 0 : sideBarSize) - (auxiliaryBarNextToEditor ? 0 : auxiliaryBarSize);

			const editorNodes = this.arrangeEditorNodes({
				editor: nodes.editor,
				sideBar: sideBarNextToEditor ? nodes.sideBar : undefined,
				auxiliaryBar: auxiliaryBarNextToEditor ? nodes.auxiliaryBar : undefined
			}, availableHeight - panelSize, editorSectionWidth);

			result.push({
				type: 'branch',
				data: panelPostion === Position.BOTTOM ? [editorNodes, nodes.panel] : [nodes.panel, editorNodes],
				size: editorSectionWidth
			});

			if (!sideBarNextToEditor) {
				if (sideBarPosition === Position.LEFT) {
					result.splice(0, 0, nodes.sideBar);
				} else {
					result.push(nodes.sideBar);
				}
			}

			if (!auxiliaryBarNextToEditor) {
				if (sideBarPosition === Position.RIGHT) {
					result.splice(0, 0, nodes.auxiliaryBar);
				} else {
					result.push(nodes.auxiliaryBar);
				}
			}

			if (sideBarPosition === Position.LEFT) {
				result.splice(0, 0, nodes.activityBar);
			} else {
				result.push(nodes.activityBar);
			}
		}

		return result;
	}

	private readonly whenReadyPromise = new DeferredPromise<void>();
	protected readonly whenReady = this.whenReadyPromise.p;

	private readonly whenRestoredPromise = new DeferredPromise<void>();
	readonly whenRestored = this.whenRestoredPromise.p;
	private restored = false;

	isRestored(): boolean {
		return this.restored;
	}

	protected restoreParts(): void {

		// distinguish long running restore operations that
		// are required for the layout to be ready from those
		// that are needed to signal restoring is done
		const layoutReadyPromises: Promise<unknown>[] = [];
		const layoutRestoredPromises: Promise<unknown>[] = [];

		// Await for promises that we recorded to update
		// our ready and restored states properly.
		Promises.settled(layoutReadyPromises).finally(() => {
			this.whenReadyPromise.complete();

			Promises.settled(layoutRestoredPromises).finally(() => {
				this.restored = true;
				this.whenRestoredPromise.complete();
			});
		});
	}
}

//#region Layout State Model

interface IWorkbenchLayoutStateKey {
	readonly name: string;
	readonly runtime: boolean;
	readonly defaultValue: unknown;
	readonly scope: StorageScope;
	readonly target: StorageTarget;
	readonly zenModeIgnore?: boolean;
}

type StorageKeyType = string | boolean | number | object;

abstract class WorkbenchLayoutStateKey<T extends StorageKeyType> implements IWorkbenchLayoutStateKey {

	abstract readonly runtime: boolean;

	constructor(readonly name: string, readonly scope: StorageScope, readonly target: StorageTarget, public defaultValue: T) { }
}

class RuntimeStateKey<T extends StorageKeyType> extends WorkbenchLayoutStateKey<T> {

	readonly runtime = true;

	constructor(name: string, scope: StorageScope, target: StorageTarget, defaultValue: T, readonly zenModeIgnore?: boolean) {
		super(name, scope, target, defaultValue);
	}
}

class InitializationStateKey<T extends StorageKeyType> extends WorkbenchLayoutStateKey<T> {
	readonly runtime = false;
}

const LayoutStateKeys = {

	// Editor
	MAIN_EDITOR_CENTERED: new RuntimeStateKey<boolean>('editor.centered', StorageScope.WORKSPACE, StorageTarget.MACHINE, false),

	// Zen Mode
	ZEN_MODE_ACTIVE: new RuntimeStateKey<boolean>('zenMode.active', StorageScope.WORKSPACE, StorageTarget.MACHINE, false),
	ZEN_MODE_EXIT_INFO: new RuntimeStateKey('zenMode.exitInfo', StorageScope.WORKSPACE, StorageTarget.MACHINE, {
		transitionedToCenteredEditorLayout: false,
		transitionedToFullScreen: false,
		handleNotificationsDoNotDisturbMode: false,
		wasVisible: {
			auxiliaryBar: false,
			panel: false,
			sideBar: false,
		},
	}),

	// Part Sizing
	GRID_SIZE: new InitializationStateKey('grid.size', StorageScope.PROFILE, StorageTarget.MACHINE, { width: 800, height: 600 }),
	SIDEBAR_SIZE: new InitializationStateKey<number>('sideBar.size', StorageScope.PROFILE, StorageTarget.MACHINE, 200),
	AUXILIARYBAR_SIZE: new InitializationStateKey<number>('auxiliaryBar.size', StorageScope.PROFILE, StorageTarget.MACHINE, 200),
	PANEL_SIZE: new InitializationStateKey<number>('panel.size', StorageScope.PROFILE, StorageTarget.MACHINE, 300),

	PANEL_LAST_NON_MAXIMIZED_HEIGHT: new RuntimeStateKey<number>('panel.lastNonMaximizedHeight', StorageScope.PROFILE, StorageTarget.MACHINE, 300),
	PANEL_LAST_NON_MAXIMIZED_WIDTH: new RuntimeStateKey<number>('panel.lastNonMaximizedWidth', StorageScope.PROFILE, StorageTarget.MACHINE, 300),
	PANEL_WAS_LAST_MAXIMIZED: new RuntimeStateKey<boolean>('panel.wasLastMaximized', StorageScope.WORKSPACE, StorageTarget.MACHINE, false),

	// Part Positions
	SIDEBAR_POSITON: new RuntimeStateKey<Position>('sideBar.position', StorageScope.WORKSPACE, StorageTarget.MACHINE, Position.LEFT),
	PANEL_POSITION: new RuntimeStateKey<Position>('panel.position', StorageScope.WORKSPACE, StorageTarget.MACHINE, Position.BOTTOM),
	PANEL_ALIGNMENT: new RuntimeStateKey<PanelAlignment>('panel.alignment', StorageScope.PROFILE, StorageTarget.USER, 'center'),

	// Part Visibility
	ACTIVITYBAR_HIDDEN: new RuntimeStateKey<boolean>('activityBar.hidden', StorageScope.WORKSPACE, StorageTarget.MACHINE, false, true),
	SIDEBAR_HIDDEN: new RuntimeStateKey<boolean>('sideBar.hidden', StorageScope.WORKSPACE, StorageTarget.MACHINE, false),
	EDITOR_HIDDEN: new RuntimeStateKey<boolean>('editor.hidden', StorageScope.WORKSPACE, StorageTarget.MACHINE, false),
	PANEL_HIDDEN: new RuntimeStateKey<boolean>('panel.hidden', StorageScope.WORKSPACE, StorageTarget.MACHINE, true),
	AUXILIARYBAR_HIDDEN: new RuntimeStateKey<boolean>('auxiliaryBar.hidden', StorageScope.WORKSPACE, StorageTarget.MACHINE, true),
	STATUSBAR_HIDDEN: new RuntimeStateKey<boolean>('statusBar.hidden', StorageScope.WORKSPACE, StorageTarget.MACHINE, false, true)

} as const;


class LayoutStateModel extends Disposable {

	private readonly stateCache = new Map<string, unknown>();

	constructor(private readonly container: HTMLElement) {
		super();
	}

	load(): void {
		let key: keyof typeof LayoutStateKeys;

		// TODO: Load stored values for all keys
		// for (key in LayoutStateKeys) {
		// 	const stateKey = LayoutStateKeys[key] as WorkbenchLayoutStateKey<StorageKeyType>;
		// 	const value = this.loadKeyFromStorage(stateKey);

		// 	if (value !== undefined) {
		// 		this.stateCache.set(stateKey.name, value);
		// 	}
		// }

		// Set dynamic defaults: part sizing and side bar visibility
		const workbenchDimensions = getClientArea(this.container);
		LayoutStateKeys.PANEL_POSITION.defaultValue = positionFromString('bottom');
		LayoutStateKeys.GRID_SIZE.defaultValue = { height: workbenchDimensions.height, width: workbenchDimensions.width };
		LayoutStateKeys.SIDEBAR_SIZE.defaultValue = Math.min(300, workbenchDimensions.width / 4);
		LayoutStateKeys.AUXILIARYBAR_SIZE.defaultValue = Math.min(300, workbenchDimensions.width / 4);
		LayoutStateKeys.PANEL_SIZE.defaultValue = (this.stateCache.get(LayoutStateKeys.PANEL_POSITION.name) ?? isHorizontal(LayoutStateKeys.PANEL_POSITION.defaultValue)) ? workbenchDimensions.height / 3 : workbenchDimensions.width / 4;
		LayoutStateKeys.SIDEBAR_HIDDEN.defaultValue = false;

		// Apply all defaults
		for (key in LayoutStateKeys) {
			const stateKey = LayoutStateKeys[key];
			if (this.stateCache.get(stateKey.name) === undefined) {
				this.stateCache.set(stateKey.name, stateKey.defaultValue);
			}
		}
	}

	getInitializationValue<T extends StorageKeyType>(key: InitializationStateKey<T>): T {
		return this.stateCache.get(key.name) as T;
	}

	setInitializationValue<T extends StorageKeyType>(key: InitializationStateKey<T>, value: T): void {
		this.stateCache.set(key.name, value);
	}

	getRuntimeValue<T extends StorageKeyType>(key: RuntimeStateKey<T>, fallbackToSetting?: boolean): T {
		// if (fallbackToSetting) {
		// 	switch (key) {
		// 		case LayoutStateKeys.ACTIVITYBAR_HIDDEN:
		// 			this.stateCache.set(key.name, this.isActivityBarHidden());
		// 			break;
		// 		case LayoutStateKeys.STATUSBAR_HIDDEN:
		// 			this.stateCache.set(key.name, !this.configurationService.getValue(LegacyWorkbenchLayoutSettings.STATUSBAR_VISIBLE));
		// 			break;
		// 		case LayoutStateKeys.SIDEBAR_POSITON:
		// 			this.stateCache.set(key.name, this.configurationService.getValue(LegacyWorkbenchLayoutSettings.SIDEBAR_POSITION) ?? 'left');
		// 			break;
		// 	}
		// }

		return this.stateCache.get(key.name) as T;
	}
}

//#endregion
