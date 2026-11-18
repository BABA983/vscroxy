package fuzzy

import (
	"slices"
	"testing"
)

// These ranking cases are ported from the compareFilesByScore / compareItemsByScore
// tests in src/vs/base/test/common/fuzzyScorer.test.ts (VS Code commit
// 972b66fdc541e10689adfd90ade9549dea1b18bc).

func assertCompareOrder(t *testing.T, paths []string, query string, want []string) {
	t.Helper()
	prepared := PrepareQuery(query)
	items := make([]item, 0, len(paths))
	for _, fullPath := range paths {
		items = append(items, resource(fullPath))
	}

	reversed := slices.Clone(items)
	slices.Reverse(reversed)
	for name, input := range map[string][]item{"ordered": items, "reversed": reversed} {
		sorted := slices.Clone(input)
		slices.SortStableFunc(sorted, func(a, b item) int {
			return CompareItemsByFuzzyScore(a, b, prepared, true, resourceAccessor{}, NewFuzzyScorerCache())
		})
		if len(want) == 1 {
			if sorted[0].path != want[0] {
				t.Fatalf("%s: %q ranked %q first, want %q (full order %v)", name, query, sorted[0].path, want[0], pathsOf(sorted))
			}
			continue
		}
		if got := pathsOf(sorted); !slices.Equal(got, want) {
			t.Fatalf("%s: %q ranked %v, want %v", name, query, got, want)
		}
	}
}

func pathsOf(items []item) []string {
	paths := make([]string, len(items))
	for i, item := range items {
		paths[i] = item.path
	}
	return paths
}

func TestCompareItemsByFuzzyScore(t *testing.T) {
	resources := []string{
		"/some/path/fileA.txt",
		"/some/path/other/fileB.txt",
		"/unrelated/some/path/other/fileC.txt",
	}
	cases := []struct {
		name  string
		paths []string
		query string
		want  []string
	}{
		{"identity A", resources, resources[0], []string{resources[0], resources[1], resources[2]}},
		{"identity B", resources, resources[1], []string{resources[1], resources[0], resources[2]}},
		{"basename prefix A", resources, "fileA.txt", []string{resources[0], resources[1], resources[2]}},
		{"basename prefix B", resources, "fileB.txt", []string{resources[1], resources[0], resources[2]}},
		{"basename camelcase A", resources, "fA", []string{resources[0], resources[1], resources[2]}},
		{"basename camelcase B", resources, "fB", []string{resources[1], resources[0], resources[2]}},
		{"basename scores A", resources, "fileA", []string{resources[0], resources[1], resources[2]}},
		{"basename scores B", resources, "fileB", []string{resources[1], resources[0], resources[2]}},
		{"path scores A", resources, "pathfileA", []string{resources[0], resources[1], resources[2]}},
		{"path scores B", resources, "pathfileB", []string{resources[1], resources[0], resources[2]}},
		{
			"shorter basenames",
			[]string{"/some/path/fileA.txt", "/some/path/other/fileBLonger.txt", "/unrelated/the/path/other/fileC.txt"},
			"somepath",
			[]string{"/some/path/fileA.txt", "/some/path/other/fileBLonger.txt", "/unrelated/the/path/other/fileC.txt"},
		},
		{
			"shorter basenames on label match",
			[]string{"/some/path/fileA.txt", "/some/path/other/fileBLonger.txt", "/unrelated/the/path/other/fileC.txt"},
			"file",
			[]string{"/some/path/fileA.txt", "/unrelated/the/path/other/fileC.txt", "/some/path/other/fileBLonger.txt"},
		},
		{"shorter paths", resources, "somepath", []string{resources[0], resources[1], resources[2]}},
		{
			"bug 17443",
			[]string{"config/test/t1.js", "config/test.js", "config/test/t2.js"},
			"co/te",
			[]string{"config/test.js", "config/test/t1.js", "config/test/t2.js"},
		},
		{
			"prefer label over description",
			[]string{"parts/quick/arrow-left-dark.svg", "parts/quickopen/quickopen.ts"},
			"partsquick",
			[]string{"parts/quickopen/quickopen.ts", "parts/quick/arrow-left-dark.svg"},
		},
		{
			"prefer camel case",
			[]string{"config/test/NullPointerException.java", "config/test/nopointerexception.java"},
			"NPE",
			[]string{"config/test/NullPointerException.java", "config/test/nopointerexception.java"},
		},
		{
			"prefer camel case lowercase",
			[]string{"config/test/NullPointerException.java", "config/test/nopointerexception.java"},
			"npe",
			[]string{"config/test/NullPointerException.java", "config/test/nopointerexception.java"},
		},
		{
			"prefer compact camel case",
			[]string{"config/test/openthisAnythingHandler.js", "config/test/openthisisnotsorelevantforthequeryAnyHand.js"},
			"AH",
			[]string{"config/test/openthisisnotsorelevantforthequeryAnyHand.js", "config/test/openthisAnythingHandler.js"},
		},
		{
			"prefer compact matches (label)",
			[]string{"config/test/examasdaple.js", "config/test/exampleasdaasd.ts"},
			"xp",
			[]string{"config/test/exampleasdaasd.ts", "config/test/examasdaple.js"},
		},
		{
			"prefer compact matches (path)",
			[]string{"config/test/examasdaple/file.js", "config/test/exampleasdaasd/file.ts"},
			"xp",
			[]string{"config/test/exampleasdaasd/file.ts", "config/test/examasdaple/file.js"},
		},
		{
			"prefer compact matches (label and path)",
			[]string{"config/example/thisfile.ts", "config/24234243244/example/file.js"},
			"exfile",
			[]string{"config/24234243244/example/file.js", "config/example/thisfile.ts"},
		},
		{
			"bug 34210",
			[]string{
				"node_modules1/bundle/lib/model/modules/ot1/index.js",
				"node_modules1/bundle/lib/model/modules/un1/index.js",
				"node_modules1/bundle/lib/model/modules/modu1/index.js",
				"node_modules1/bundle/lib/model/modules/oddl1/index.js",
			},
			"modu1/index.js",
			[]string{"node_modules1/bundle/lib/model/modules/modu1/index.js"},
		},
		{
			"bug 21019 1",
			[]string{
				"app/containers/Services/NetworkData/ServiceDetails/ServiceLoad/index.js",
				"app/containers/Services/NetworkData/ServiceDetails/ServiceDistribution/index.js",
				"app/containers/Services/NetworkData/ServiceDetailTabs/ServiceTabs/StatVideo/index.js",
			},
			"StatVideoindex",
			[]string{"app/containers/Services/NetworkData/ServiceDetailTabs/ServiceTabs/StatVideo/index.js"},
		},
		{
			"bug 21019 2",
			[]string{"src/build-helper/store/redux.ts", "src/repository/store/redux.ts"},
			"reproreduxts",
			[]string{"src/repository/store/redux.ts"},
		},
		{
			"bug 26649",
			[]string{
				"photobook/src/components/AddPagesButton/index.js",
				"photobook/src/components/ApprovalPageHeader/index.js",
				"photobook/src/canvasComponents/BookPage/index.js",
			},
			"bookpageIndex",
			[]string{"photobook/src/canvasComponents/BookPage/index.js"},
		},
		{
			"bug 33247",
			[]string{"ui/src/utils/constants.js", "ui/src/ui/Icons/index.js"},
			"ui/icons",
			[]string{"ui/src/ui/Icons/index.js"},
		},
		{
			"bug 33247 comment",
			[]string{"ui/src/components/IDInput/index.js", "ui/src/ui/Input/index.js"},
			"ui/input/index",
			[]string{"ui/src/ui/Input/index.js"},
		},
		{
			"bug 36166",
			[]string{"django/contrib/sites/locale/ga/LC_MESSAGES/django.mo", "django/core/signals.py"},
			"djancosig",
			[]string{"django/core/signals.py"},
		},
		{
			"bug 32918",
			[]string{
				"adsys/protected/config.php",
				"adsys/protected/framework/smarty/sysplugins/smarty_internal_config.php",
				"duowanVideo/wap/protected/config.php",
			},
			"protectedconfig.php",
			[]string{"adsys/protected/config.php", "duowanVideo/wap/protected/config.php", "adsys/protected/framework/smarty/sysplugins/smarty_internal_config.php"},
		},
		{
			"bug 14879",
			[]string{"pkg/search/gradient/testdata/constraint_attrMatchString.yml", "cmd/gradient/main.go"},
			"gradientmain",
			[]string{"cmd/gradient/main.go"},
		},
		{
			"bug 14727 1",
			[]string{"alpha-beta-cappa.txt", "abc.txt"},
			"abc",
			[]string{"abc.txt"},
		},
		{
			"bug 14727 2",
			[]string{"xerxes-yak-zubba/index.js", "xyz/index.js"},
			"xyz",
			[]string{"xyz/index.js"},
		},
		{
			"bug 18381",
			[]string{"AssymblyInfo.cs", "IAsynchronousTask.java"},
			"async",
			[]string{"IAsynchronousTask.java"},
		},
		{
			"bug 35572",
			[]string{
				"static/app/source/angluar/-admin/-organization/-settings/layout/layout.js",
				"static/app/source/angular/-admin/-project/-settings/_settings/settings.js",
			},
			"partisettings",
			[]string{"static/app/source/angular/-admin/-project/-settings/_settings/settings.js"},
		},
		{
			"bug 36810",
			[]string{
				"Trilby.TrilbyTV.Web.Portal/Views/Systems/Index.cshtml",
				"Trilby.TrilbyTV.Web.Portal/Areas/Admins/Views/Tips/Index.cshtml",
			},
			"tipsindex.cshtml",
			[]string{"Trilby.TrilbyTV.Web.Portal/Areas/Admins/Views/Tips/Index.cshtml"},
		},
		{
			"bug 20546",
			[]string{"editor/core/components/tests/list-view-spec.js", "editor/core/components/list-view.js"},
			"listview",
			[]string{"editor/core/components/list-view.js"},
		},
		{
			"bug 12095",
			[]string{
				"src/vs/workbench/contrib/files/common/explorerViewModel.ts",
				"src/vs/workbench/contrib/files/browser/views/explorerView.ts",
				"src/vs/workbench/contrib/files/browser/views/explorerViewer.ts",
			},
			"filesexplorerview.ts",
			[]string{"src/vs/workbench/contrib/files/browser/views/explorerView.ts"},
		},
		{
			"bug 96122",
			[]string{"lists.php", "lib/Lists.php"},
			"Lists.php",
			[]string{"lib/Lists.php"},
		},
		{
			"bug 103052 foo bar",
			[]string{"app/emails/foo.bar.js", "app/emails/other-footer.other-bar.js"},
			"foo bar",
			[]string{"app/emails/foo.bar.js", "app/emails/other-footer.other-bar.js"},
		},
		{
			"bug 103052 foobar",
			[]string{"app/emails/foo.bar.js", "app/emails/other-footer.other-bar.js"},
			"foobar",
			[]string{"app/emails/foo.bar.js", "app/emails/other-footer.other-bar.js"},
		},
		{
			"bug 103052 payment model",
			[]string{"app/components/payment/payment.model.js", "app/components/online-payments-history/online-payments-history.model.js"},
			"payment model",
			[]string{"app/components/payment/payment.model.js", "app/components/online-payments-history/online-payments-history.model.js"},
		},
		{
			"bug 103052 paymentmodel",
			[]string{"app/components/payment/payment.model.js", "app/components/online-payments-history/online-payments-history.model.js"},
			"paymentmodel",
			[]string{"app/components/payment/payment.model.js", "app/components/online-payments-history/online-payments-history.model.js"},
		},
		{
			"bug 103052 color js",
			[]string{"app/constants/color.js", "app/components/model/input/pick-avatar-color.js"},
			"color js",
			[]string{"app/constants/color.js", "app/components/model/input/pick-avatar-color.js"},
		},
		{
			"bug 103052 colorjs",
			[]string{"app/constants/color.js", "app/components/model/input/pick-avatar-color.js"},
			"colorjs",
			[]string{"app/constants/color.js", "app/components/model/input/pick-avatar-color.js"},
		},
		{
			"strict case prefix",
			[]string{"app/constants/color.js", "app/components/model/input/Color.js"},
			"Color",
			[]string{"app/components/model/input/Color.js", "app/constants/color.js"},
		},
		{
			"strict case prefix lowercase query",
			[]string{"app/constants/color.js", "app/components/model/input/Color.js"},
			"color",
			[]string{"app/constants/color.js", "app/components/model/input/Color.js"},
		},
		{
			"prefix bug 103052",
			[]string{"test/smoke/src/main.ts", "src/vs/editor/common/services/semantikTokensProviderStyling.ts"},
			"smoke main.ts",
			[]string{"test/smoke/src/main.ts", "src/vs/editor/common/services/semantikTokensProviderStyling.ts"},
		},
		{
			"boost better prefix multiple",
			[]string{"src/vs/workbench/services/host/browser/browserHostService.ts", "src/vs/workbench/browser/workbench.ts"},
			"workbench.ts browser",
			[]string{"src/vs/workbench/browser/workbench.ts", "src/vs/workbench/services/host/browser/browserHostService.ts"},
		},
		{
			"boost better prefix multiple (browser first)",
			[]string{"src/vs/workbench/services/host/browser/browserHostService.ts", "src/vs/workbench/browser/workbench.ts"},
			"browser workbench.ts",
			[]string{"src/vs/workbench/browser/workbench.ts", "src/vs/workbench/services/host/browser/browserHostService.ts"},
		},
		{
			"boost better prefix multiple (short)",
			[]string{"src/vs/workbench/services/host/browser/browserHostService.ts", "src/vs/workbench/browser/workbench.ts"},
			"browser workbench",
			[]string{"src/vs/workbench/browser/workbench.ts", "src/vs/workbench/services/host/browser/browserHostService.ts"},
		},
		{
			"boost better prefix multiple (workbench browser)",
			[]string{"src/vs/workbench/services/host/browser/browserHostService.ts", "src/vs/workbench/browser/workbench.ts"},
			"workbench browser",
			[]string{"src/vs/workbench/browser/workbench.ts", "src/vs/workbench/services/host/browser/browserHostService.ts"},
		},
		{
			"boost shorter prefix multiple",
			[]string{"src/vs/workbench/node/actions/windowActions.ts", "src/vs/workbench/electron-node/window.ts"},
			"window node",
			[]string{"src/vs/workbench/electron-node/window.ts", "src/vs/workbench/node/actions/windowActions.ts"},
		},
		{
			"boost shorter prefix multiple with extension",
			[]string{"src/vs/workbench/node/actions/windowActions.ts", "src/vs/workbench/electron-node/window.ts"},
			"window.ts node",
			[]string{"src/vs/workbench/electron-node/window.ts", "src/vs/workbench/node/actions/windowActions.ts"},
		},
		{
			"skip label preference with path separator",
			[]string{"djangosite/ufrela/def.py", "djangosite/urls/default.py"},
			"url/def",
			[]string{"djangosite/urls/default.py", "djangosite/ufrela/def.py"},
		},
		{
			"boost shorter prefix #99171",
			[]string{"mesh_editor_lifetime_job.h", "lifetime_job.h"},
			"m life, life m",
			[]string{"lifetime_job.h", "mesh_editor_lifetime_job.h"},
		},
		{
			"boost consecutive matches at beginning",
			[]string{"src/vs/server/node/extensionHostStatusService.ts", "src/vs/workbench/browser/parts/notifications/notificationsStatus.ts"},
			"notStatus",
			[]string{"src/vs/workbench/browser/parts/notifications/notificationsStatus.ts", "src/vs/server/node/extensionHostStatusService.ts"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCompareOrder(t, tc.paths, tc.query, tc.want)
		})
	}
}
