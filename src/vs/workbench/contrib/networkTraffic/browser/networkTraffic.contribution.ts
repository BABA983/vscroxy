import { localize, localize2 } from '../../../../nls.js';
import { SyncDescriptor } from '../../../../platform/instantiation/common/descriptors.js';
import { Registry, Extensions as ViewContainerExtensions } from '../../../../platform/registry/common/platform.js';
import { ViewPaneContainer } from '../../../browser/parts/views/viewPaneContainer.js';
import { IViewContainerDescriptor, IViewContainersRegistry, ViewContainerLocation } from '../../../common/views.js';
import { NetworkTrafficDetail } from '../common/networkTraffic.js';

const NETWORK_TRAFFIC_DETAIL_CONTAINER: IViewContainerDescriptor = Registry.as<IViewContainersRegistry>(ViewContainerExtensions.ViewContainersRegistry)
	.registerViewContainer({
		id: NetworkTrafficDetail.VIEW_CONTAINER_ID,
		title: localize2('network.traffic.detail.title', "Network Traffic Detail"),
		order: 0,
		ctorDescriptor: new SyncDescriptor(ViewPaneContainer, [NetworkTrafficDetail.VIEW_CONTAINER_ID, { mergeViewWithContainerWhenSingleView: true }]),
	}, ViewContainerLocation.Panel, { doNotRegisterOpenCommand: true });
