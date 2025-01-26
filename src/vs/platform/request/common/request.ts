/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { streamToBuffer } from '../../../base/common/buffer.js';
import { CancellationToken } from '../../../base/common/cancellation.js';
import { getErrorMessage } from '../../../base/common/errors.js';
import { Disposable } from '../../../base/common/lifecycle.js';
import { IHeaders, IRequestContext, IRequestOptions } from '../../../base/parts/request/common/request.js';
import { createDecorator } from '../../instantiation/common/instantiation.js';
import { ILogService } from '../../log/common/log.js';

export const IRequestService = createDecorator<IRequestService>('requestService');

export interface AuthInfo {
	isProxy: boolean;
	scheme: string;
	host: string;
	port: number;
	realm: string;
	attempt: number;
}

export interface Credentials {
	username: string;
	password: string;
}

export interface IRequestService {
	readonly _serviceBrand: undefined;

	request(options: IRequestOptions, token: CancellationToken): Promise<IRequestContext>;

	resolveProxy(url: string): Promise<string | undefined>;
	lookupAuthorization(authInfo: AuthInfo): Promise<Credentials | undefined>;
	lookupKerberosAuthorization(url: string): Promise<string | undefined>;
	loadCertificates(): Promise<string[]>;
}

class LoggableHeaders {

	private headers: IHeaders | undefined;

	constructor(private readonly original: IHeaders) { }

	toJSON(): any {
		if (!this.headers) {
			const headers = Object.create(null);
			for (const key in this.original) {
				if (key.toLowerCase() === 'authorization' || key.toLowerCase() === 'proxy-authorization') {
					headers[key] = '*****';
				} else {
					headers[key] = this.original[key];
				}
			}
			this.headers = headers;
		}
		return this.headers;
	}

}

export abstract class AbstractRequestService extends Disposable implements IRequestService {

	declare readonly _serviceBrand: undefined;

	private counter = 0;

	constructor(protected readonly logService: ILogService) {
		super();
	}

	protected async logAndRequest(options: IRequestOptions, request: () => Promise<IRequestContext>): Promise<IRequestContext> {
		const prefix = `#${++this.counter}: ${options.url}`;
		this.logService.trace(`${prefix} - begin`, options.type, new LoggableHeaders(options.headers ?? {}));
		try {
			const result = await request();
			this.logService.trace(`${prefix} - end`, options.type, result.res.statusCode, result.res.headers);
			return result;
		} catch (error) {
			this.logService.error(`${prefix} - error`, options.type, getErrorMessage(error));
			throw error;
		}
	}

	abstract request(options: IRequestOptions, token: CancellationToken): Promise<IRequestContext>;
	abstract resolveProxy(url: string): Promise<string | undefined>;
	abstract lookupAuthorization(authInfo: AuthInfo): Promise<Credentials | undefined>;
	abstract lookupKerberosAuthorization(url: string): Promise<string | undefined>;
	abstract loadCertificates(): Promise<string[]>;
}

export function isSuccess(context: IRequestContext): boolean {
	return (context.res.statusCode && context.res.statusCode >= 200 && context.res.statusCode < 300) || context.res.statusCode === 1223;
}

export function hasNoContent(context: IRequestContext): boolean {
	return context.res.statusCode === 204;
}

export async function asText(context: IRequestContext): Promise<string | null> {
	if (hasNoContent(context)) {
		return null;
	}
	const buffer = await streamToBuffer(context.stream);
	return buffer.toString();
}

export async function asTextOrError(context: IRequestContext): Promise<string | null> {
	if (!isSuccess(context)) {
		throw new Error('Server returned ' + context.res.statusCode);
	}
	return asText(context);
}

export async function asJson<T = {}>(context: IRequestContext): Promise<T | null> {
	if (!isSuccess(context)) {
		throw new Error('Server returned ' + context.res.statusCode);
	}
	if (hasNoContent(context)) {
		return null;
	}
	const buffer = await streamToBuffer(context.stream);
	const str = buffer.toString();
	try {
		return JSON.parse(str);
	} catch (err) {
		err.message += ':\n' + str;
		throw err;
	}
}

