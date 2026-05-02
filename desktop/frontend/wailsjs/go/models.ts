export namespace main {

	export class AppConfig {
	    workspace: string;
	    token: string;
	    codexBinary: string;
	    host: string;
	    port: number;
	    codexHost: string;
	    codexPort: number;
	    boundThreadId: string;
	    clientPingIntervalMs: number;
	    clientIdleTimeoutMs: number;
	    lastConnectionBaseUrl: string;

	    static createFrom(source: any = {}) {
	        return new AppConfig(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = source["workspace"];
	        this.token = source["token"];
	        this.codexBinary = source["codexBinary"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.codexHost = source["codexHost"];
	        this.codexPort = source["codexPort"];
	        this.boundThreadId = source["boundThreadId"];
	        this.clientPingIntervalMs = source["clientPingIntervalMs"];
	        this.clientIdleTimeoutMs = source["clientIdleTimeoutMs"];
	        this.lastConnectionBaseUrl = source["lastConnectionBaseUrl"];
	    }
	}
	export class GatewayStatus {
	    running: boolean;
	    gateway: string;
	    appServer: string;
	    threadId: string;
	    defaultThreadId: string;
	    cwd: string;
	    activeTurnId: string;
	    error: string;
	    configPath: string;
	    connectionUrl: string;
	    timestamp: number;

	    static createFrom(source: any = {}) {
	        return new GatewayStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.gateway = source["gateway"];
	        this.appServer = source["appServer"];
	        this.threadId = source["threadId"];
	        this.defaultThreadId = source["defaultThreadId"];
	        this.cwd = source["cwd"];
	        this.activeTurnId = source["activeTurnId"];
	        this.error = source["error"];
	        this.configPath = source["configPath"];
	        this.connectionUrl = source["connectionUrl"];
	        this.timestamp = source["timestamp"];
	    }
	}
	export class AppSnapshot {
	    config: AppConfig;
	    status: GatewayStatus;

	    static createFrom(source: any = {}) {
	        return new AppSnapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.config = this.convertValues(source["config"], AppConfig);
	        this.status = this.convertValues(source["status"], GatewayStatus);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class ThreadSummary {
	    id: string;
	    name: string;
	    preview: string;
	    cwd: string;
	    updatedAt: number;

	    static createFrom(source: any = {}) {
	        return new ThreadSummary(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.preview = source["preview"];
	        this.cwd = source["cwd"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}
