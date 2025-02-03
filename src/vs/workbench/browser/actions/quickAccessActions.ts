import { Codicon } from '../../../base/common/codicons.js';
import { ServicesAccessor } from '../../../editor/browser/editorExtensions.js';
import { localize } from '../../../nls.js';
import { Action2, MenuId, registerAction2 } from '../../../platform/actions/common/actions.js';
import { ILogService } from '../../../platform/log/common/log.js';

registerAction2(class QuickAccessAction extends Action2 {
	constructor() {
		super({
			id: 'workbench.action.quickOpenWithModes',
			title: localize('quickOpenWithModes', "Quick Open"),
			icon: Codicon.search,
			menu: {
				id: MenuId.CommandCenterCenter,
				order: 100
			}
		});
	}

	run(accessor: ServicesAccessor): void {
		const logService = accessor.get(ILogService);
		logService.info('Trigger quick open');
	}
});
