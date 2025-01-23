const glob = require('glob');
const path = require('path');

require('tsx/cjs/api').register();

// Re-export all .ts files as rules
const rules = {};
glob.sync(`${__dirname}/*.ts`).forEach((file) => {
	rules[path.basename(file, '.ts')] = require(file);
});

exports.rules = rules;
