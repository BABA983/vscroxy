import { Disposable } from '../../base/common/lifecycle.js';
import { IContextKey, IContextKeyService } from '../../platform/contextkey/common/contextkey.js';
import { PanelMaximizedContext } from '../common/contextkeys.js';
import { IWorkbenchLayoutService } from '../services/layout/browser/layoutService.js';

export class WorkbenchContextKeysHandler extends Disposable {

	private panelMaximizedContext: IContextKey<boolean>;

	constructor(
		@IContextKeyService private readonly contextKeyService: IContextKeyService,
		@IWorkbenchLayoutService private readonly layoutService: IWorkbenchLayoutService,
	) {
		super();

		// Panel
		this.panelMaximizedContext = PanelMaximizedContext.bindTo(this.contextKeyService);
		this.panelMaximizedContext.set(this.layoutService.isPanelMaximized());

		this.registerListeners();
	}

	private registerListeners(): void {
		this._register(this.layoutService.onDidChangePartVisibility(() => {
			this.panelMaximizedContext.set(this.layoutService.isPanelMaximized());
		}));
	}


}
