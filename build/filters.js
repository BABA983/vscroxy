const { readFileSync } = require('fs');
const { join } = require('path');

module.exports.eslintFilter = [
	'**/*.js',
	'**/*.cjs',
	'**/*.mjs',
	'**/*.ts',
	...readFileSync(join(__dirname, '..', '.eslint-ignore'))
		.toString()
		.split(/\r\n|\n/)
		.filter(line => line && !line.startsWith('#'))
		.map(line => line.startsWith('!') ? line.slice(1) : `!${line}`)
];
