import path from 'node:path';
import { afterAll, afterEach, beforeAll, beforeEach, suite, test, } from 'vitest';

globalThis._VSCODE_FILE_ROOT = path.join(__dirname);
(globalThis.suite as any) = suite;
// (globalThis.test as any) = test;
(globalThis.test as any) = Object.assign(function patchedTest(name: string, fn?: Function) {
	if (fn) {
		return test(name, function (this: any, ...args) {
			const context = this || {};
			context.skip = test.skip;
			context.currentTest = globalThis.currentTest;
			context.timeout = (ms: number) => {
				setTimeout(() => {
					throw new Error(`Timeout after ${ms}...`);
				}, ms);
			};
			return fn.apply(context, args);
		});
	}
	return test(name, fn);
}, test);
(globalThis.test.skip as any) = test.skip;
(globalThis.setup as any) = beforeEach;
(globalThis.suiteSetup as any) = beforeAll;
// (globalThis.teardown as any) = afterEach;
(globalThis.teardown as any) = function (fn: Function) {
	return afterEach(function (this: any, ...args) {
		const context = this || {};
		context.currentTest = globalThis.currentTest;
		return fn.apply(context, args);
	});
};
(globalThis.suiteTeardown as any) = afterAll;
(globalThis.skip as any) = test.skip;

(globalThis.currentTest as any) = {
	state: undefined
};
beforeEach((ctx) => {
	ctx.onTestFailed((result) => {
		const _map = {
			pass: 'passed',
			fail: 'failed',
		};
		(globalThis.currentTest as any).state = _map[result.state] || undefined;
	});
});

process.on('uncaughtException', (e) => {
	if (e.message === 'Error: done() callback is deprecated, use promise instead') {
		// noop
	} else {
		console.error(e);
	}
});
