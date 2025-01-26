import { app, crashReporter, protocol } from 'electron';
import minimist from 'minimist';
import * as fs from 'original-fs';
import * as path from 'path';
import { performance } from 'perf_hooks';
import { fileURLToPath } from 'url';
import { bootstrapESM } from './bootstrap-esm.js';
import { product } from './bootstrap-meta.js';
import * as perf from './vs/base/common/performance.js';
import { NativeParsedArgs } from './vs/platform/environment/common/argv.js';
import { getUserDataPath } from './vs/platform/environment/node/userDataPath.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

console.log(product);

perf.mark('code/didStartMain');

perf.mark('code/willLoadMainBundle', {
	// When built, the main bundle is a single JS file with all
	// dependencies inlined. As such, we mark `willLoadMainBundle`
	// as the start of the main bundle loading process.
	startTime: Math.floor(performance.timeOrigin)
});
perf.mark('code/didLoadMainBundle');


const args = parseCLIArgs();

// Enable sandbox globally unless
// 1) disabled via command line using either
//    `--no-sandbox` or `--disable-chromium-sandbox` argument.
// 2) argv.json contains `disable-chromium-sandbox: true`.
if (args['sandbox'] &&
	!args['disable-chromium-sandbox']) {
	app.enableSandbox();
} else if (app.commandLine.hasSwitch('no-sandbox') &&
	!app.commandLine.hasSwitch('disable-gpu-sandbox')) {
	// Disable GPU sandbox whenever --no-sandbox is used.
	app.commandLine.appendSwitch('disable-gpu-sandbox');
} else {
	app.commandLine.appendSwitch('no-sandbox');
	app.commandLine.appendSwitch('disable-gpu-sandbox');
}

// Set userData path before app 'ready' event
const userDataPath = getUserDataPath(args, product.nameShort ?? 'vscroxy-dev');
app.setPath('userData', userDataPath);

// Configure crash reporter
perf.mark('code/willStartCrashReporter');
// If a crash-reporter-directory is specified we store the crash reports
// in the specified directory and don't upload them to the crash server.
//
// Appcenter crash reporting is enabled if
// * enable-crash-reporter runtime argument is set to 'true'
// * --disable-crash-reporter command line parameter is not set
//
// Disable crash reporting in all other cases.
if (args['crash-reporter-directory']) {
	configureCrashReporter();
}
perf.mark('code/didStartCrashReporter');

// Register custom schemes with privileges
protocol.registerSchemesAsPrivileged([
	{
		scheme: 'vscode-webview',
		privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, allowServiceWorkers: true, codeCache: true }
	},
	{
		scheme: 'vscode-file',
		privileges: { secure: true, standard: true, supportFetchAPI: true, corsEnabled: true, codeCache: true }
	}
]);

app.once('ready', function () {
	onReady();
});

async function onReady() {
	perf.mark('code/mainAppReady');

	try {
		await startup();
	} catch (error) {
		console.error(error);
	}
}

async function startup(): Promise<void> {
	await bootstrapESM();

	// Load Main
	await import('./vs/croxy/electron-main/main.js');
	perf.mark('code/didRunMainBundle');
}

function configureCrashReporter(): void {
	let crashReporterDirectory = args['crash-reporter-directory'];
	const submitURL = '';
	if (crashReporterDirectory) {
		crashReporterDirectory = path.normalize(crashReporterDirectory);

		if (!path.isAbsolute(crashReporterDirectory)) {
			console.error(`The path '${crashReporterDirectory}' specified for --crash-reporter-directory must be absolute.`);
			app.exit(1);
		}

		if (!fs.existsSync(crashReporterDirectory)) {
			try {
				fs.mkdirSync(crashReporterDirectory, { recursive: true });
			} catch (error) {
				console.error(`The path '${crashReporterDirectory}' specified for --crash-reporter-directory does not seem to exist or cannot be created.`);
				app.exit(1);
			}
		}

		// Crashes are stored in the crashDumps directory by default, so we
		// need to change that directory to the provided one
		console.log(`Found --crash-reporter-directory argument. Setting crashDumps directory to be '${crashReporterDirectory}'`);
		app.setPath('crashDumps', crashReporterDirectory);
	}

	// Start crash reporter for all processes
	const productName = (product.crashReporter ? product.crashReporter.productName : undefined) || product.nameShort;
	const companyName = (product.crashReporter ? product.crashReporter.companyName : undefined) || 'Microsoft';
	const uploadToServer = Boolean(!process.env['VSCODE_DEV'] && submitURL && !crashReporterDirectory);
	crashReporter.start({
		companyName,
		productName: process.env['VSCODE_DEV'] ? `${productName} Dev` : productName,
		submitURL,
		uploadToServer,
		compress: true
	});
}

function parseCLIArgs(): NativeParsedArgs {
	return minimist(process.argv, {
		string: [
			'user-data-dir',
			'locale',
			'js-flags',
			'crash-reporter-directory'
		],
		boolean: [
			'disable-chromium-sandbox',
		],
		default: {
			'sandbox': true
		},
		alias: {
			'no-sandbox': 'sandbox'
		}
	});
}

