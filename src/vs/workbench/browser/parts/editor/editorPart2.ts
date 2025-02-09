import './media/editorpart2.css';
import { Dimension, $, append } from '../../../../base/browser/dom.js';
import { HighlightedLabel } from '../../../../base/browser/ui/highlightedlabel/highlightedLabel.js';
import { ITableRenderer, ITableVirtualDelegate } from '../../../../base/browser/ui/table/table.js';
import { Table } from '../../../../base/browser/ui/table/tableWidget.js';
import { mainWindow } from '../../../../base/browser/window.js';
import { Disposable } from '../../../../base/common/lifecycle.js';
import { localize } from '../../../../nls.js';
import { IInstantiationService } from '../../../../platform/instantiation/common/instantiation.js';
import { IStorageService } from '../../../../platform/storage/common/storage.js';
import { defaultListStyles } from '../../../../platform/theme/browser/defaultStyles.js';
import { IThemeService } from '../../../../platform/theme/common/themeService.js';
import { IWorkbenchLayoutService, Parts } from '../../../services/layout/browser/layoutService.js';
import { Part } from '../../part.js';

class NetworkTraffics {
	constructor() { }
}

export class NetworkTrafficTableItem {
	constructor(
		public readonly method: string,
		public readonly url: string,
		public readonly remoteIP: string,
		public readonly duration: number,
		public readonly statusCode?: number,
		public readonly size?: number,
	) {

	}
}

export interface IMarkerHighlightedLabelColumnTemplateData {
	readonly columnElement: HTMLElement;
	readonly highlightedLabel: HighlightedLabel;
}

class NetworkTrafficMethodColumnRenderer implements ITableRenderer<NetworkTrafficTableItem, IMarkerHighlightedLabelColumnTemplateData> {
	static readonly TEMPLATE_ID = 'method';

	readonly templateId: string = NetworkTrafficMethodColumnRenderer.TEMPLATE_ID;
	renderTemplate(container: HTMLElement) {
		const columnElement = append(container, $('.method'));
		const highlightedLabel = new HighlightedLabel(columnElement);
		return { columnElement, highlightedLabel };
	}
	renderElement(element: NetworkTrafficTableItem, index: number, templateData: IMarkerHighlightedLabelColumnTemplateData, height: number | undefined): void {
		templateData.columnElement.title = element.method;
		// templateData.highlightedLabel.element.textContent = element.method;
		templateData.highlightedLabel.set(element.method, undefined);
	}

	disposeTemplate(templateData: IMarkerHighlightedLabelColumnTemplateData): void {
		templateData.highlightedLabel.dispose();
	}

}
class NetworkTrafficUrlColumnRenderer implements ITableRenderer<NetworkTrafficTableItem, IMarkerHighlightedLabelColumnTemplateData> {
	static readonly TEMPLATE_ID = 'url';

	readonly templateId: string = NetworkTrafficUrlColumnRenderer.TEMPLATE_ID;
	renderTemplate(container: HTMLElement) {
		const columnElement = append(container, $('.url'));
		const highlightedLabel = new HighlightedLabel(columnElement);
		return { columnElement, highlightedLabel };
	}
	renderElement(element: NetworkTrafficTableItem, index: number, templateData: IMarkerHighlightedLabelColumnTemplateData, height: number | undefined): void {
		templateData.columnElement.title = element.url;
		templateData.highlightedLabel.set(element.url, undefined);
		// templateData.highlightedLabel.element.textContent = element.url;
		// templateData.highlightedLabel.set(element.method, element.method);
	}

	disposeTemplate(templateData: IMarkerHighlightedLabelColumnTemplateData): void {
		templateData.highlightedLabel.dispose();
	}

}

class NetworkTrafficIpColumnRenderer implements ITableRenderer<NetworkTrafficTableItem, IMarkerHighlightedLabelColumnTemplateData> {
	static readonly TEMPLATE_ID = 'remoteIP';

	readonly templateId: string = NetworkTrafficIpColumnRenderer.TEMPLATE_ID;
	renderTemplate(container: HTMLElement) {
		const columnElement = append(container, $('.remote-ip'));
		const highlightedLabel = new HighlightedLabel(columnElement);
		return { columnElement, highlightedLabel };
	}
	renderElement(element: NetworkTrafficTableItem, index: number, templateData: IMarkerHighlightedLabelColumnTemplateData, height: number | undefined): void {
		templateData.columnElement.title = element.remoteIP;
		templateData.highlightedLabel.set(element.remoteIP, undefined);
		// templateData.highlightedLabel.element.textContent = element.url;
		// templateData.highlightedLabel.set(element.method, element.method);
	}

	disposeTemplate(templateData: IMarkerHighlightedLabelColumnTemplateData): void {
		templateData.highlightedLabel.dispose();
	}

}

class NetworkTrafficDurationColumnRenderer implements ITableRenderer<NetworkTrafficTableItem, IMarkerHighlightedLabelColumnTemplateData> {
	static readonly TEMPLATE_ID = 'duration';

	readonly templateId: string = NetworkTrafficDurationColumnRenderer.TEMPLATE_ID;
	renderTemplate(container: HTMLElement) {
		const columnElement = append(container, $('.duration'));
		const highlightedLabel = new HighlightedLabel(columnElement);
		return { columnElement, highlightedLabel };
	}
	renderElement(element: NetworkTrafficTableItem, index: number, templateData: IMarkerHighlightedLabelColumnTemplateData, height: number | undefined): void {
		templateData.columnElement.title = element.duration.toString() || '-';
		templateData.highlightedLabel.set(element.duration.toString() || '-', undefined);
		// templateData.highlightedLabel.element.textContent = element.url;
		// templateData.highlightedLabel.set(element.method, element.method);
	}

	disposeTemplate(templateData: IMarkerHighlightedLabelColumnTemplateData): void {
		templateData.highlightedLabel.dispose();
	}

}

class NetworkTrafficStatusCodeColumnRenderer implements ITableRenderer<NetworkTrafficTableItem, IMarkerHighlightedLabelColumnTemplateData> {
	static readonly TEMPLATE_ID = 'statusCode';

	readonly templateId: string = NetworkTrafficStatusCodeColumnRenderer.TEMPLATE_ID;
	renderTemplate(container: HTMLElement) {
		const columnElement = append(container, $('.statusCode'));
		const highlightedLabel = new HighlightedLabel(columnElement);
		return { columnElement, highlightedLabel };
	}
	renderElement(element: NetworkTrafficTableItem, index: number, templateData: IMarkerHighlightedLabelColumnTemplateData, height: number | undefined): void {
		templateData.columnElement.title = element.statusCode?.toString() || '-';
		templateData.highlightedLabel.set(element.statusCode?.toString() || '-', undefined);
		// templateData.highlightedLabel.element.textContent = element.url;
		// templateData.highlightedLabel.set(element.method, element.method);
	}

	disposeTemplate(templateData: IMarkerHighlightedLabelColumnTemplateData): void {
		templateData.highlightedLabel.dispose();
	}

}

class NetworkTrafficSizeColumnRenderer implements ITableRenderer<NetworkTrafficTableItem, IMarkerHighlightedLabelColumnTemplateData> {
	static readonly TEMPLATE_ID = 'size';

	readonly templateId: string = NetworkTrafficSizeColumnRenderer.TEMPLATE_ID;
	renderTemplate(container: HTMLElement) {
		const columnElement = append(container, $('.size'));
		const highlightedLabel = new HighlightedLabel(columnElement);
		return { columnElement, highlightedLabel };
	}
	renderElement(element: NetworkTrafficTableItem, index: number, templateData: IMarkerHighlightedLabelColumnTemplateData, height: number | undefined): void {
		templateData.columnElement.title = element.size?.toString() || '-';
		templateData.highlightedLabel.set(element.size?.toString() || '-', undefined);
		// templateData.highlightedLabel.element.textContent = element.url;
		// templateData.highlightedLabel.set(element.method, element.method);
	}

	disposeTemplate(templateData: IMarkerHighlightedLabelColumnTemplateData): void {
		templateData.highlightedLabel.dispose();
	}

}

class NetworkTrafficTableVirtualDelegate implements ITableVirtualDelegate<any> {
	static readonly HEADER_ROW_HEIGHT = 24;
	static readonly ROW_HEIGHT = 24;
	readonly headerRowHeight = NetworkTrafficTableVirtualDelegate.HEADER_ROW_HEIGHT;

	getHeight(item: any) {
		return NetworkTrafficTableVirtualDelegate.ROW_HEIGHT;
	}
}

class NetworkTrafficTable extends Disposable {
	private readonly table: Table<any>;

	constructor(
		private readonly container: HTMLElement,
		@IInstantiationService private readonly instantiationService: IInstantiationService,
	) {
		super();

		this.table = this.instantiationService.createInstance(Table,
			'NetworkTraffic',
			this.container,
			new NetworkTrafficTableVirtualDelegate(),
			[
				{
					label: localize('method', 'Method'),
					weight: 0,
					minimumWidth: 60,
					maximumWidth: 60,
					templateId: NetworkTrafficMethodColumnRenderer.TEMPLATE_ID,
					project(row) { return row; },
				},
				{
					label: localize('url', 'URL'),
					weight: 2,
					templateId: NetworkTrafficUrlColumnRenderer.TEMPLATE_ID,
					project(row) { return row; },
				},
				{
					label: localize('remoteIP', 'Remote IP'),
					weight: 1,
					templateId: NetworkTrafficIpColumnRenderer.TEMPLATE_ID,
					project(row) { return row; },
				},
				{
					label: localize('duration', 'Duration'),
					weight: 1,
					minimumWidth: 80,
					maximumWidth: 80,
					templateId: NetworkTrafficDurationColumnRenderer.TEMPLATE_ID,
					project(row) { return row; },
				},
				{
					label: localize('statusCode', 'Code'),
					weight: 0,
					minimumWidth: 50,
					maximumWidth: 50,
					templateId: NetworkTrafficStatusCodeColumnRenderer.TEMPLATE_ID,
					project(row) { return row; },
				},
				{
					label: localize('size', 'Size'),
					weight: 0,
					minimumWidth: 50,
					maximumWidth: 50,
					templateId: NetworkTrafficSizeColumnRenderer.TEMPLATE_ID,
					project(row) { return row; },
				},
			],
			[
				this.instantiationService.createInstance(NetworkTrafficMethodColumnRenderer),
				this.instantiationService.createInstance(NetworkTrafficUrlColumnRenderer),
				this.instantiationService.createInstance(NetworkTrafficIpColumnRenderer),
				this.instantiationService.createInstance(NetworkTrafficDurationColumnRenderer),
				this.instantiationService.createInstance(NetworkTrafficStatusCodeColumnRenderer),
				this.instantiationService.createInstance(NetworkTrafficSizeColumnRenderer),
			],
			{
				horizontalScrolling: true,
			}
		);

		this.table.style(defaultListStyles);

		// this.table.splice(0, Number.POSITIVE_INFINITY, [
		// 	new NetworkTrafficTableItem('GET', 'https://github.com/BABA983/vscroxy')
		// ]);
		this.table.splice(0, Number.POSITIVE_INFINITY, Array.from({ length: 1000 }, (_, i) => new NetworkTrafficTableItem('GET', 'https://github.com/BABA983/vscroxy', '127.0.0.1', 1000, 200, 1000)));
	}

	layout(width: number, height: number) {
		this.container.style.height = `${height}px`;
		this.table.layout(height, width);
	}
}

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

	private table!: NetworkTrafficTable;

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

	private createTable(parent: HTMLElement) {
		const table = this.instantiationService.createInstance(NetworkTrafficTable,
			append(parent, $('.network-traffic-table-container'))
		);

		return table;
	}

	protected override createContentArea(parent: HTMLElement, options?: object): HTMLElement | undefined {
		this.element = parent;
		this.container = document.createElement('div');
		this.container.classList.add('content');

		parent.appendChild(this.container);
		this.table = this.createTable(this.container);

		return this.container;
	}

	override layout(width: number, height: number, top: number, left: number): void {
		this.top = top;
		this.left = left;

		super.layoutContents(width, height);

		this.table.layout(width, height);
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

