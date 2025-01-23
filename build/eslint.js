/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

const es = require('event-stream');
const vfs = require('vinyl-fs');
const { eslintFilter } = require('./filters');

function eslint() {
	const eslint = require('./gulp-eslint');
	const s = vfs
		.src(eslintFilter, { base: '.', follow: true, allowEmpty: true })
		.pipe(
			eslint((results) => {
				if (results.warningCount > 0 || results.errorCount > 0) {
					s.emit('error', new Error('eslint failed with warnings and/or errors'));
				}
			})
		).pipe(es.through(function () { /* noop, important for the stream to end */ }));
	return s;
}

if (require.main === module) {
	eslint().on('error', (err) => {
		console.error(err);
		process.exit(1);
	});
}
