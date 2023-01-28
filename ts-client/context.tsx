import { v4 as uuidv4 } from "uuid"
import dateParser from "./jsonParser"
import { Message } from "./message"
import { protocol } from "./util"

export type Observer = (state: number) => void
export type Subscriber = (msg: Message) => void

export interface WSConn {
    socket: WebSocket
    ident: string
    lastMessage?: Message
    host?: string
    subscribers: { [key: string]: Subscriber[] }
    binarySubscribers: { [key: string]: Subscribers }
    observers: Observer[]
    isCommander: boolean
}

export interface Subscribers {
    binaryParser: (data: ArrayBuffer) => Message
    subscribers: Subscriber[]
}

interface WSHost {
    isPending: boolean
    pending: (uri: string, pending?: boolean) => boolean
    connections: { [key: string]: WSConn }
    host: string
}

interface InitArgs {
    defaultHost: string
}

export const Initialize = ({ defaultHost }: InitArgs) => {
    hosts.push(NewWSHost(defaultHost))
}

const NewWSHost = (hostname: string): WSHost => {
    return {
        isPending: false,
        pending: (uri: string, isPending?: boolean) => {
            const host = Host(hostname)
            if (isPending !== undefined) {
                host.isPending = isPending
            }
            return host.isPending
        },
        connections: {},
        host: hostname,
    }
}

const hosts: WSHost[] = []

const Host = (hostname: string): WSHost => {
    let wshost = hosts.find((wsh) => {
        return wsh.host === hostname
    })
    if (!wshost) {
        wshost = NewWSHost(hostname)
        hosts.push(wshost)
    }
    return wshost
}
if (typeof window !== "undefined") {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(window as any).ws = hosts
}

export const DefaultHost = (): string => hosts[0].host

const deslash = (s: string): string => {
    return s.startsWith("/") ? s.substring(1) : s
}

export const Socket = (URI: WSUri, exact?: boolean): WebSocket | undefined => {
    if (exact) {
        const wshost = Host(URI.host)
        const conn = wshost.connections[URI.path]
        if (!conn) return undefined
        return conn.socket
    }
    const [, root] = FindRoot(URI)
    return root ? root.socket : undefined
}

export const RemoveConnections = (URI: WSUri) => {
    const wshost = Host(URI.host)
    if (!wshost) return

    const key = Object.keys(wshost.connections).find((key: string) => {
        return wshost.connections[key] && !wshost.connections[key].isCommander && URI.path.startsWith(key)
    })

    if (key && wshost.connections[key]) {
        wshost.connections[key].observers = []
        wshost.connections[key].subscribers = {}
        wshost.connections[key].binarySubscribers = {}
    }
}

export interface WSUri {
    path: string
    absolute: string
    host: string
}

export interface UrifyArgs {
    URI: string
    host: string
}

export const Urify = ({ URI, host }: UrifyArgs): WSUri => {
    return {
        absolute: `${protocol()}://${host}/api/ws/${deslash(URI)}`,
        host,
        path: `/${deslash(URI)}`,
    }
}

interface ConnectArgs {
    uri: WSUri
    ident: string
    observer?: Observer
    isCommander?: boolean
}

interface SubscribeArgs {
    uri: WSUri
    sub: Subscriber
    observer?: Observer
    isCommander?: boolean
    binaryKey?: number
    binaryParser?: (data: ArrayBuffer) => Message
}

export const Subscribe = ({ uri, sub, observer, isCommander, binaryKey, binaryParser }: SubscribeArgs): Promise<() => void> => {
    const ident = uuidv4()

    return new Promise<() => void>((resolve) => {
        Connect({ uri, ident, observer, isCommander }).then((cleanup) => {
            let conn: WSConn | undefined
            let rootKey: string
            if (isCommander) {
                const wshost = Host(uri.host)
                conn = wshost.connections[uri.path]
            } else {
                const fr = FindRoot(uri)
                rootKey = fr[0]
                conn = fr[1]
            }
            if (!conn) {
                throw new Error("conn disappeared")
            }

            // build map
            if (binaryKey && binaryParser) {
                if (conn.binarySubscribers[binaryKey]) {
                    if (conn.binarySubscribers[binaryKey].subscribers.indexOf(sub) === -1) {
                        conn.binarySubscribers[binaryKey].subscribers.push(sub)
                    }
                } else {
                    conn.binarySubscribers[binaryKey] = {
                        binaryParser,
                        subscribers: [sub],
                    }
                }

                if (conn.binarySubscribers[binaryKey].subscribers.length === 1) {
                    // send subscribe request
                    try {
                        conn.socket.send(`SUBSCRIBE:${uri.path}`)
                    } catch (err) {
                        console.error(err)
                    }
                }
            } else {
                if (conn.subscribers[uri.path]) {
                    if (conn.subscribers[uri.path].indexOf(sub) === -1) {
                        conn.subscribers[uri.path].push(sub)
                    }
                } else {
                    conn.subscribers[uri.path] = [sub]
                }

                // send subscribe request
                try {
                    if (!isCommander) {
                        conn.socket.send(`SUBSCRIBE:${uri.path}`)
                    }
                } catch (err) {
                    console.error(err)
                }
            }

            resolve(() => {
                const wshost = Host(uri.host)
                const conn = wshost.connections[rootKey ? rootKey : uri.path]
                if (conn) {
                    if (binaryKey) {
                        if (conn.binarySubscribers[binaryKey]) {
                            const index = conn.binarySubscribers[binaryKey].subscribers.indexOf(sub)

                            if (index > -1) {
                                conn.binarySubscribers[binaryKey].subscribers.splice(index, 1)
                            }

                            if (conn.binarySubscribers[binaryKey].subscribers.length === 0) {
                                conn.socket.send(`UNSUBSCRIBE:${uri.path}`)
                            }
                        }
                    }

                    // normal subscriber uri
                    if (conn.subscribers[uri.path]) {
                        const index = conn.subscribers[uri.path].indexOf(sub)

                        if (index > -1) {
                            conn.subscribers[uri.path].splice(index, 1)
                        }

                        if (conn.subscribers[uri.path].length === 0) {
                            conn.socket.send(`UNSUBSCRIBE:${uri.path}`)
                        }
                    }
                }

                cleanup()
            })
        })
    })
}

const configureSocketEvents = (path: string, socket: WebSocket, isCommander: boolean, uri: WSUri, observer?: Observer) => {
    const wshost = Host(uri.host)
    const conn = wshost.connections[path]
    if (!conn) {
        wshost.connections[path] = {
            socket,
            ident: "",
            observers: [],
            subscribers: {},
            binarySubscribers: {},
            host: uri.host,
            isCommander,
        }
    } else {
        wshost.connections[path].socket = socket
    }

    const stateChange = () => {
        try {
            wshost.connections[path]?.observers.forEach((ob) => {
                ob(wshost.connections[path].socket.readyState)
            })
        } catch (err) {
            console.warn(err)
        }
    }

    socket.onopen = stateChange
    socket.onerror = (err) => {
        console.error(err)
        stateChange()
    }
    socket.onclose = () => {
        stateChange()
    }
    socket.onmessage = (message) => {
        if (!wshost.connections[path]) throw new Error("ws state has changed")
        try {
            if (!message.data) return

            // if binary data
            if (message.data instanceof Blob) {
                message.data.arrayBuffer().then((data) => {
                    const dv = new DataView(data)
                    const type = dv.getUint8(0)

                    // reverse json struct
                    if (type === 0) {
                        const enc = new TextDecoder("utf-8")
                        const arr = new Uint8Array(data)
                        const payload = enc.decode(arr).substring(1)

                        // parse to json
                        const msgData: Message = JSON.parse(payload, dateParser)
                        msgData.mt = window.performance.now()

                        if (wshost.connections[path]) {
                            wshost.connections[path].lastMessage = msgData
                        }
                        // broadcast through normal subscribers
                        if (wshost.connections[path]?.subscribers[msgData.uri]) {
                            wshost.connections[path].subscribers[msgData.uri].forEach((sub) => sub(msgData))
                        }
                        return
                    }

                    // parse as binary
                    if (wshost.connections[path]?.binarySubscribers[type]) {
                        const msgData: Message = wshost.connections[path].binarySubscribers[type].binaryParser(data)
                        msgData.key = type
                        wshost.connections[path].binarySubscribers[type].subscribers.forEach((sub) => sub(msgData))
                    }
                })

                return
            }

            // if json
            const msgData: Message = JSON.parse(message.data, dateParser)
            msgData.mt = window.performance.now()

            if (wshost.connections[path]) {
                wshost.connections[path].lastMessage = msgData
            }
            // broadcast through normal subscribers
            if (wshost.connections[path].subscribers[msgData.uri]) {
                wshost.connections[path].subscribers[msgData.uri].forEach((sub) => sub(msgData))
            }
        } catch (err) {
            console.log(err)
            return
        }
    }

    if (observer) {
        wshost.connections[path].observers.push(observer)
        observer(wshost.connections[path].socket.readyState)
    }

    if (!isCommander) {
        wshost.pending(path, false)
    }
}

const FindRoot = (uri: WSUri): [string, WSConn | undefined] => {
    const wshost = Host(uri.host)
    if (!wshost) {
        console.log("no wshost")
        return [uri.path, undefined]
    }
    const key = Object.keys(wshost.connections).find((key: string) => {
        return wshost.connections[key] && !wshost.connections[key].isCommander && uri.path.startsWith(key)
    })
    if (key && wshost.connections[key]) {
        return [key, wshost.connections[key]]
    }
    return [uri.path, undefined]
}

export const Connect = ({ uri, ident, observer, isCommander }: ConnectArgs): Promise<() => void> => {
    // move the subscription on root websocket connection
    const wshost = Host(uri.host)
    const isPending = wshost.pending(uri.path)

    if (isPending && !isCommander) {
        return new Promise<() => void>((resolve) => {
            setTimeout(() => {
                Connect({ uri, ident, observer }).then((cleanup) => {
                    resolve(cleanup)
                })
            }, 500)
        })
    }

    if (!isCommander) {
        wshost.pending(uri.path, true)
    }
    const cleanup = () => {
        const [rootKey, conn] = FindRoot(uri)
        if (!conn) return
        if (observer) {
            const index = conn.observers.indexOf(observer)
            if (index > -1) {
                conn.observers.splice(index, 1)
            }
        }

        // clean up ws if no observers exist
        if (conn.observers.length === 0) {
            try {
                conn.socket.send("exit")
                console.info(`closing comms channel ${rootKey}`)
                conn.socket.close()
                console.log("socket closed!")
            } catch (err) {
                console.error(err)
            }
            delete wshost.connections[rootKey]
        }
    }

    return new Promise((resolve, reject) => {
        const [rootKey, conn] = isCommander ? [uri.path, wshost.connections[uri.path]] : FindRoot(uri)
        // check connection
        if (!conn || (conn.socket.readyState !== WebSocket.CONNECTING && conn.socket.readyState !== WebSocket.OPEN)) {
            try {
                const socket = new WebSocket(uri.absolute)
                if (!isCommander) {
                    socket.onclose = () => {
                        observer && observer(WebSocket.CLOSED)
                        wshost.pending(uri.path, false)
                    }
                    socket.onerror = () => {
                        wshost.pending(uri.path, false)
                    }

                    socket.onmessage = (message) => {
                        if (!message.data.startsWith("ROOT:")) {
                            socket.close()
                            console.error("no root provided")
                            return
                        }

                        const rootKey: string = message.data.slice(5)
                        configureSocketEvents(rootKey, socket, !!isCommander, uri, observer)
                        resolve(cleanup)
                    }
                } else {
                    configureSocketEvents(rootKey, socket, isCommander, uri, observer)
                    resolve(cleanup)
                }
            } catch (e) {
                console.log("error from ws connection", e)
                wshost.pending(rootKey, false)
                cleanup()
                reject(e)
            }
        } else {
            if (observer) {
                wshost.connections[rootKey].observers.push(observer)
                observer(wshost.connections[rootKey].socket.readyState)

                if (!isCommander) {
                    wshost.pending(rootKey, false)
                }

                resolve(cleanup)
            }
        }
    })
}
