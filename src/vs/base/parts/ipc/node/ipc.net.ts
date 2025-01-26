// Read this before there's any chance it is overwritten
// Related to https://github.com/microsoft/vscode/issues/30624
export const XDG_RUNTIME_DIR = <string | undefined>process.env['XDG_RUNTIME_DIR'];
