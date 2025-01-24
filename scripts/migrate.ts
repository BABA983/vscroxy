// *****************************************************************
// *                                                               *
// *               VSCODE MIGRATION SCRIPT                         *
// *                                                               *
// *****************************************************************
import { readdirSync, statSync, readFileSync, existsSync, mkdirSync, writeFileSync } from 'node:fs';
import * as path from 'node:path';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { minimatch } from 'minimatch';

const migrateEslintPluginLocal = process.argv.includes('--eslint-plugin-local');
const migrateBaseCommon = process.argv.includes('--base-common');
const migrateBaseCommonTest = process.argv.includes('--base-common-test');
const migrateSelfHostTestProvider = process.argv.includes('--self-host-test-provider');
const migrateBuildLib = process.argv.includes('--build-lib');

const __dirname = path.dirname(fileURLToPath(new URL(import.meta.url)));

const rootFolder = path.join(__dirname, '..');
const vscodeFolder = path.join(rootFolder, '..', 'vscode');
const srcFolder = path.join(vscodeFolder, 'src');

const ignores = readFileSync(path.join(rootFolder, '.gitignore'), 'utf8')
	.toString()
	.split(/\r\n|\n/)
	.filter(line => line && !line.startsWith('#'));

function migrate() {
	console.log(`~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~`);
	/** @type {string[]} */
	const files = [];
	readdir(srcFolder, files);
}

function readdir(dirPath, result) {
	const entries = readdirSync(dirPath);
	for (const entry of entries) {
		const entryPath = join(dirPath, entry);
		const stat = statSync(entryPath);
		if (stat.isDirectory()) {
			readdir(join(dirPath, entry), result);
		} else {
			result.push(entryPath);
		}
	}
}

function buffersAreEqual(existingFileContents: Buffer | undefined, fileContents: Buffer | string) {
	if (!existingFileContents) {
		return false;
	}
	if (typeof fileContents === 'string') {
		fileContents = Buffer.from(fileContents);
	}
	return existingFileContents.equals(fileContents);
}

const ensureDirCache = new Set();
function ensureDir(dirPath: string) {
	if (ensureDirCache.has(dirPath)) {
		return;
	}
	ensureDirCache.add(dirPath);
	ensureDir(dirname(dirPath));
	if (!existsSync(dirPath)) {
		mkdirSync(dirPath);
	}
}

function writeDestFile(srcFilePath, fileContents) {
	const dirPath = path.dirname(srcFilePath);
	const fileName = path.basename(srcFilePath);
	const destFilePath = path.join(dirPath.replace('vscode', 'vscroxy'), fileName);
	ensureDir(dirname(destFilePath));
	let existingFileContents: Buffer | undefined = undefined;
	try {
		existingFileContents = readFileSync(destFilePath);
	} catch (err) { }
	if (!buffersAreEqual(existingFileContents, fileContents)) {
		writeFileSync(destFilePath, fileContents);
	}
}

const files: string[] = [];


// TODO: Maybe we should use git patch?
if (migrateEslintPluginLocal) {
	const eslintPluginLocalSrcFolder = path.join(vscodeFolder, '.eslint-plugin-local');
	const eslintPluginLocalDstFolder = path.join(rootFolder, '.eslint-plugin-local');

	readdir(eslintPluginLocalSrcFolder, files);
	const files2 = files.filter(file => !ignores.some(ignore => minimatch(file, ignore)));
	for (const filePath of files2) {
		const content = readFileSync(filePath);
		const lines = content.toString().split(/\r\n|\n/);
		let didChange = false;
		for (let i = 0; i < lines.length; i++) {
			const line = lines[i];
			if (/import minimatch from 'minimatch';/.test(line)) {
				didChange = true;
				lines[i] = `import { minimatch } from 'minimatch';`;
				console.log('Patch minimatch successfully...');
			} else if (/^require\('ts-node'\)/.test(line)) {
				didChange = true;
				lines[i] = `require('tsx/cjs/api').register();`;
				console.log('Patch ts-node successfully...');
			} else if (/const { dependencies, optionalDependencies } = require\(join\(__dirname, '..\/package\.json'\)\);/.test(line)) {
				didChange = true;
				lines[i] = `\t\t\tconst { dependencies, optionalDependencies = {} } = require(join(__dirname, '../package.json'));`;
				console.log('Patch optionalDep = {} successfully...');
			}
		}
		const fileContents = didChange ? lines.join('\n') : content;
		writeDestFile(path.join(eslintPluginLocalDstFolder, path.basename(filePath)), fileContents);
	}
}

if (migrateBaseCommon) {
	const srcFolder = path.join(vscodeFolder, 'src', 'vs', 'base', 'common');

	readdir(srcFolder, files);

	const files2 = files.filter(file => !ignores.some(ignore => minimatch(file, ignore)));
	for (const filePath of files2) {
		const content = readFileSync(filePath);
		writeDestFile(filePath, content);
	}
}

if (migrateBaseCommonTest) {
	const srcFolder = path.join(vscodeFolder, 'src', 'vs', 'base', 'test', 'common');

	readdir(srcFolder, files);

	const files2 = files.filter(file => !ignores.some(ignore => minimatch(file, ignore)));
	for (const filePath of files2) {
		const content = readFileSync(filePath);
		writeDestFile(filePath, content);
	}
}

if (migrateSelfHostTestProvider) {
	const srcFolder = path.join(vscodeFolder, '.vscode', 'extensions', 'vscode-selfhost-test-provider');

	readdir(srcFolder, files);

	const files2 = files.filter(file => !ignores.some(ignore => minimatch(file, ignore)));
	for (const filePath of files2) {
		const content = readFileSync(filePath);
		writeDestFile(filePath, content);
	}
}
if (migrateBuildLib) {
	const srcFolder = path.join(vscodeFolder, 'build', 'lib');

	readdir(srcFolder, files);

	const files2 = files.filter(file => !ignores.some(ignore => minimatch(file, ignore)));
	for (const filePath of files2) {
		const content = readFileSync(filePath);
		writeDestFile(filePath, content);
	}
}
