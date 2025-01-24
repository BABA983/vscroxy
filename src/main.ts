import { app, BrowserWindow } from 'electron';

console.log('Hello VSCroxy');

app.whenReady().then(() => {
	const win = new BrowserWindow();

	win.on('ready-to-show', () => win.show());
});
