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

const __dirname = path.dirname(fileURLToPath(new URL(import.meta.url)));

const rootFolder = path.join(__dirname, '..');
const vscodeFolder = path.join(rootFolder, '..', 'vscode');
const srcFolder = path.join(vscodeFolder, 'src');

const ESLINT_PLUGIN_LOCAL_FOLDER = '.eslint-plugin-local';
const eslintPluginLocalSrcFolder = path.join(vscodeFolder, ESLINT_PLUGIN_LOCAL_FOLDER);
const eslintPluginLocalDstFolder = path.join(rootFolder, ESLINT_PLUGIN_LOCAL_FOLDER);

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

/** @type {string[]} */
const files = [];

if (migrateEslintPluginLocal) {
	readdir(eslintPluginLocalSrcFolder, files);
	const eslintPluginLocalFiles = files.filter(file => !ignores.some(ignore => minimatch(file, ignore)));
	for (const filePath of eslintPluginLocalFiles) {
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
			}
		}
		ensureDir(dirname(eslintPluginLocalDstFolder));
		let existingFileContents: Buffer | undefined = undefined;
		try {
			existingFileContents = readFileSync(eslintPluginLocalDstFolder);
		} catch (err) { }
		const fileContents = didChange ? lines.join('\n') : content;
		if (!buffersAreEqual(existingFileContents, fileContents)) {
			writeFileSync(path.join(eslintPluginLocalDstFolder, path.basename(filePath)), fileContents);
		}

	}
}
