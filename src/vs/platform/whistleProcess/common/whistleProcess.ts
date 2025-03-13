export const WhistleProcessLifecycle = {
	exit: `vscode:electron-main->whistle-process=exit`,
	ipcReady: `vscode:whistle-process->electron-main=ipc-ready`,
	initDone: `vscode:whistle-process->electron-main=init-done`,
};

export const WhistleProcessChannelConnection = {
	request: `vscode:electron-main->whistle-process=create-whistle-process-channel-connection`,
	response: `vscode:electron-main->whistle-process=create-whistle-process-channel-connection-result`,
};

export const WhistleProcessRawConnection = {
	request: `vscode:electron-main->whistle-process=create-whistle-process-raw-connection`,
	response: `vscode:electron-main->whistle-process=create-whistle-process-raw-connection-result`,
};
