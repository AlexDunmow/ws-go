import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { v4 as uuidv4 } from "uuid"
import { Connect, DefaultHost, RemoveConnections, Socket, Subscribe, Subscriber, Urify } from "./context"
import { Message } from "./message"
import { useMounted } from "./useMounted"

interface WSProps {
    URI: string
    key?: string
    host?: string
    ready?: boolean
    subscriber?: Subscriber
    isCommander?: boolean
    binaryKey?: number
    binaryParser?: (data: ArrayBuffer) => Message
}

const MAX_RECONNECT_ATTEMPTS = 6

export const useWS = ({ URI, host, ready = true, subscriber, isCommander, binaryKey, binaryParser }: WSProps) => {
    const wsURI = useMemo(() => Urify({ URI, host: host || DefaultHost() }), [URI, host])
    const isBrowser = typeof window !== "undefined"
    const socket = Socket(wsURI)
    const ident = useRef<string>(uuidv4())
    const cleanupConn = useRef<() => void>(() => {
        return
    })

    const isMounted = useMounted()
    const [socketReady, setSocketReady] = useState<boolean>(false)
    const [socketState, setSocketState] = useState<number>(isBrowser ? WebSocket.CONNECTING : 0)
    const [isReconnecting, setIsReconnecting] = useState(false)
    const [isServerDown, setIsServerDown] = useState(true)
    const timeUntilRetry = useRef(6)
    const reconnectAttempts = useRef(0)

    const makeConnection = useCallback(() => {
        if (!ready) return
        if (subscriber) {
            Subscribe({
                uri: wsURI,
                sub: (msg) => subscriber(msg),
                observer: setSocketState,
                isCommander,
                binaryKey,
                binaryParser,
            }).then((cleanup) => {
                cleanupConn.current = cleanup
                if (isMounted()) setSocketReady(true)
            })
        } else {
            Connect({
                uri: wsURI,
                ident: ident.current,
                observer: setSocketState,
                isCommander,
            }).then((cleanup) => {
                cleanupConn.current = cleanup
                if (isMounted()) setSocketReady(true)
            })
        }

        // Return a clean up function
        return () => {
            const cleanup = cleanupConn.current
            cleanup()
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [URI, host, ready, wsURI])

    useEffect(() => {
        return makeConnection()
    }, [makeConnection])

    // Reconnect
    useEffect(() => {
        if (!isMounted()) return

        // Will attempt to reconnect if ws closed
        if (socketState === WebSocket.CLOSED && reconnectAttempts.current < MAX_RECONNECT_ATTEMPTS) {
            setIsReconnecting(true)
            RemoveConnections(wsURI)

            setTimeout(() => {
                reconnectAttempts.current += 1
                console.log(`Reconnect attempt: ${reconnectAttempts.current}/${MAX_RECONNECT_ATTEMPTS}`)
                setSocketState(isBrowser ? WebSocket.CONNECTING : 0)
                makeConnection()
                timeUntilRetry.current += 6
            }, timeUntilRetry.current * 1000)
        } else if (socketState === WebSocket.CLOSED) {
            setIsServerDown(true)
            setIsReconnecting(false)
        } else if (socketState === WebSocket.OPEN) {
            timeUntilRetry.current = 6
            reconnectAttempts.current = 0
            setIsReconnecting(false)
            setIsServerDown(false)
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [socketState])

    return {
        state: !isBrowser ? 0 : !socketReady ? WebSocket.CONNECTING : socketState,
        isReconnecting,
        isServerDown,
        key: wsURI,
        socket,
    }
}
