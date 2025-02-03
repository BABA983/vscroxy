import { BrowserFeatures, KeyboardSupport } from '../../../../base/browser/canIUse.js';
import { mainWindow } from '../../../../base/browser/window.js';
import { Event } from '../../../../base/common/event.js';
import { Keybinding, KeyCodeChord, ResolvedKeybinding, ScanCodeChord } from '../../../../base/common/keybindings.js';
import { IContextKeyServiceTarget, IContextKeyService } from '../../../../platform/contextkey/common/contextkey.js';
import { InstantiationType, registerSingleton } from '../../../../platform/instantiation/common/extensions.js';
import { AbstractKeybindingService } from '../../../../platform/keybinding/common/abstractKeybindingService.js';
import { IKeybindingService, IKeyboardEvent, KeybindingsSchemaContribution } from '../../../../platform/keybinding/common/keybinding.js';
import { KeybindingResolver, ResolutionResult } from '../../../../platform/keybinding/common/keybindingResolver.js';
import { IKeybindingItem, KeybindingsRegistry } from '../../../../platform/keybinding/common/keybindingsRegistry.js';
import { ResolvedKeybindingItem } from '../../../../platform/keybinding/common/resolvedKeybindingItem.js';
import * as browser from '../../../../base/browser/browser.js';
import { IMMUTABLE_CODE_TO_KEY_CODE, KeyCode, KeyCodeUtils, KeyMod, ScanCode, ScanCodeUtils } from '../../../../base/common/keyCodes.js';
import { OperatingSystem, OS } from '../../../../base/common/platform.js';
import { ICommandService } from '../../../../platform/commands/common/commands.js';
import { INotificationService } from '../../../../platform/notification/common/notification.js';
import { ILogService } from '../../../../platform/log/common/log.js';
import { IKeyboardMapper } from '../../../../platform/keyboardLayout/common/keyboardMapper.js';

export class WorkbenchKeybindingService extends AbstractKeybindingService {

	private _cachedResolver: KeybindingResolver | null = null;
	private _keyboardMapper: IKeyboardMapper;

	constructor(
		@IContextKeyService contextKeyService: IContextKeyService,
		@ICommandService commandService: ICommandService,
		@INotificationService notificationService: INotificationService,
		@ILogService logService: ILogService,
	) {
		super(contextKeyService, commandService, notificationService, logService);
	}

	protected _getResolver(): KeybindingResolver {
		if (!this._cachedResolver) {
			const defaults = this._resolveKeybindingItems(KeybindingsRegistry.getDefaultKeybindings(), true);
			// const overrides = this._resolveUserKeybindingItems(this.userKeybindings.keybindings, false);
			this._cachedResolver = new KeybindingResolver(defaults, [], (str) => this._log(str));
		}
		return this._cachedResolver;
	}

	private _resolveKeybindingItems(items: IKeybindingItem[], isDefault: boolean): ResolvedKeybindingItem[] {
		const result: ResolvedKeybindingItem[] = [];
		let resultLen = 0;
		for (const item of items) {
			const when = item.when || undefined;
			const keybinding = item.keybinding;
			if (!keybinding) {
				// This might be a removal keybinding item in user settings => accept it
				result[resultLen++] = new ResolvedKeybindingItem(undefined, item.command, item.commandArgs, when, isDefault, item.extensionId, item.isBuiltinExtension);
			} else {
				if (this._assertBrowserConflicts(keybinding)) {
					continue;
				}

				// const resolvedKeybindings = this._keyboardMapper.resolveKeybinding(keybinding);
				// for (let i = resolvedKeybindings.length - 1; i >= 0; i--) {
				// 	const resolvedKeybinding = resolvedKeybindings[i];
				// 	result[resultLen++] = new ResolvedKeybindingItem(resolvedKeybinding, item.command, item.commandArgs, when, isDefault, item.extensionId, item.isBuiltinExtension);
				// }
			}
		}

		return result;
	}

	private _assertBrowserConflicts(keybinding: Keybinding): boolean {
		if (BrowserFeatures.keyboard === KeyboardSupport.Always) {
			return false;
		}

		if (BrowserFeatures.keyboard === KeyboardSupport.FullScreen && browser.isFullscreen(mainWindow)) {
			return false;
		}

		for (const chord of keybinding.chords) {
			if (!chord.metaKey && !chord.altKey && !chord.ctrlKey && !chord.shiftKey) {
				continue;
			}

			const modifiersMask = KeyMod.CtrlCmd | KeyMod.Alt | KeyMod.Shift;

			let partModifiersMask = 0;
			if (chord.metaKey) {
				partModifiersMask |= KeyMod.CtrlCmd;
			}

			if (chord.shiftKey) {
				partModifiersMask |= KeyMod.Shift;
			}

			if (chord.altKey) {
				partModifiersMask |= KeyMod.Alt;
			}

			if (chord.ctrlKey && OS === OperatingSystem.Macintosh) {
				partModifiersMask |= KeyMod.WinCtrl;
			}

			if ((partModifiersMask & modifiersMask) === (KeyMod.CtrlCmd | KeyMod.Alt)) {
				if (chord instanceof ScanCodeChord && (chord.scanCode === ScanCode.ArrowLeft || chord.scanCode === ScanCode.ArrowRight)) {
					// console.warn('Ctrl/Cmd+Arrow keybindings should not be used by default in web. Offender: ', kb.getHashCode(), ' for ', commandId);
					return true;
				}
				if (chord instanceof KeyCodeChord && (chord.keyCode === KeyCode.LeftArrow || chord.keyCode === KeyCode.RightArrow)) {
					// console.warn('Ctrl/Cmd+Arrow keybindings should not be used by default in web. Offender: ', kb.getHashCode(), ' for ', commandId);
					return true;
				}
			}

			if ((partModifiersMask & modifiersMask) === KeyMod.CtrlCmd) {
				if (chord instanceof ScanCodeChord && (chord.scanCode >= ScanCode.Digit1 && chord.scanCode <= ScanCode.Digit0)) {
					// console.warn('Ctrl/Cmd+Num keybindings should not be used by default in web. Offender: ', kb.getHashCode(), ' for ', commandId);
					return true;
				}
				if (chord instanceof KeyCodeChord && (chord.keyCode >= KeyCode.Digit0 && chord.keyCode <= KeyCode.Digit9)) {
					// console.warn('Ctrl/Cmd+Num keybindings should not be used by default in web. Offender: ', kb.getHashCode(), ' for ', commandId);
					return true;
				}
			}
		}

		return false;
	}


}

registerSingleton(IKeybindingService, WorkbenchKeybindingService, InstantiationType.Eager);
