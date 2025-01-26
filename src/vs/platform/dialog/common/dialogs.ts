import { mnemonicButtonLabel } from '../../../base/common/labels.js';
import { deepClone } from '../../../base/common/objects.js';
import { isLinux, isMacintosh, isWindows } from '../../../base/common/platform.js';
import { MessageBoxOptions } from '../../../base/parts/sandbox/common/electronTypes.js';
import { IProductService } from '../../product/common/productService.js';

export interface IMassagedMessageBoxOptions {

	/**
	 * OS massaged message box options.
	 */
	readonly options: MessageBoxOptions;

	/**
	 * Since the massaged result of the message box options potentially
	 * changes the order of buttons, we have to keep a map of these
	 * changes so that we can still return the correct index to the caller.
	 */
	readonly buttonIndeces: number[];
}

/**
 * A utility method to ensure the options for the message box dialog
 * are using properties that are consistent across all platforms and
 * specific to the platform where necessary.
 */
export function massageMessageBoxOptions(options: MessageBoxOptions, productService: IProductService): IMassagedMessageBoxOptions {
	const massagedOptions = deepClone(options);

	let buttons = (massagedOptions.buttons ?? []).map(button => mnemonicButtonLabel(button));
	let buttonIndeces = (options.buttons || []).map((button, index) => index);

	let defaultId = 0; // by default the first button is default button
	let cancelId = massagedOptions.cancelId ?? buttons.length - 1; // by default the last button is cancel button

	// Apply HIG per OS when more than one button is used
	if (buttons.length > 1) {
		const cancelButton = typeof cancelId === 'number' ? buttons[cancelId] : undefined;

		if (isLinux || isMacintosh) {

			// Linux: the GNOME HIG (https://developer.gnome.org/hig/patterns/feedback/dialogs.html?highlight=dialog)
			// recommend the following:
			// "Always ensure that the cancel button appears first, before the affirmative button. In left-to-right
			//  locales, this is on the left. This button order ensures that users become aware of, and are reminded
			//  of, the ability to cancel prior to encountering the affirmative button."
			//
			// Electron APIs do not reorder buttons for us, so we ensure a reverse order of buttons and a position
			// of the cancel button (if provided) that matches the HIG

			// macOS: the HIG (https://developer.apple.com/design/human-interface-guidelines/components/presentation/alerts)
			// recommend the following:
			// "Place buttons where people expect. In general, place the button people are most likely to choose on the trailing side in a
			//  row of buttons or at the top in a stack of buttons. Always place the default button on the trailing side of a row or at the
			//  top of a stack. Cancel buttons are typically on the leading side of a row or at the bottom of a stack."
			//
			// However: it seems that older macOS versions where 3 buttons were presented in a row differ from this
			// recommendation. In fact, cancel buttons were placed to the left of the default button and secondary
			// buttons on the far left. To support these older macOS versions we have to manually shuffle the cancel
			// button in the same way as we do on Linux. This will not have any impact on newer macOS versions where
			// shuffling is done for us.

			if (typeof cancelButton === 'string' && buttons.length > 1 && cancelId !== 1) {
				buttons.splice(cancelId, 1);
				buttons.splice(1, 0, cancelButton);

				const cancelButtonIndex = buttonIndeces[cancelId];
				buttonIndeces.splice(cancelId, 1);
				buttonIndeces.splice(1, 0, cancelButtonIndex);

				cancelId = 1;
			}

			if (isLinux && buttons.length > 1) {
				buttons = buttons.reverse();
				buttonIndeces = buttonIndeces.reverse();

				defaultId = buttons.length - 1;
				if (typeof cancelButton === 'string') {
					cancelId = defaultId - 1;
				}
			}
		} else if (isWindows) {

			// Windows: the HIG (https://learn.microsoft.com/en-us/windows/win32/uxguide/win-dialog-box)
			// recommend the following:
			// "One of the following sets of concise commands: Yes/No, Yes/No/Cancel, [Do it]/Cancel,
			//  [Do it]/[Don't do it], [Do it]/[Don't do it]/Cancel."
			//
			// Electron APIs do not reorder buttons for us, so we ensure the position of the cancel button
			// (if provided) that matches the HIG

			if (typeof cancelButton === 'string' && buttons.length > 1 && cancelId !== buttons.length - 1 /* last action */) {
				buttons.splice(cancelId, 1);
				buttons.push(cancelButton);

				const buttonIndex = buttonIndeces[cancelId];
				buttonIndeces.splice(cancelId, 1);
				buttonIndeces.push(buttonIndex);

				cancelId = buttons.length - 1;
			}
		}
	}

	massagedOptions.buttons = buttons;
	massagedOptions.defaultId = defaultId;
	massagedOptions.cancelId = cancelId;
	massagedOptions.noLink = true;
	massagedOptions.title = massagedOptions.title || productService.nameLong;

	return {
		options: massagedOptions,
		buttonIndeces
	};
}
