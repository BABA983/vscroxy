import { createDecorator } from '../../instantiation/common/instantiation.js';

export const ILayoutService = createDecorator<ILayoutService>('layoutService');

export interface ILayoutService {
	readonly _serviceBrand: undefined;
}
