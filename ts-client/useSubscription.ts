import { useState } from "react"
import { useWS } from "./useWS"
import { Message } from "./message"

export interface SubProps {
    URI: string
    key?: string
    host?: string
    ready?: boolean
    binaryKey?: number
    binaryParser?: (data: ArrayBuffer) => Message
}

export const useSubscription = <T = unknown>({ URI, key = "", host, ready, binaryKey, binaryParser }: SubProps, callback?: (payload: T) => void) => {
    const [payload, setPayload] = useState<T>()
    useWS({
        URI,
        key,
        host,
        ready,
        binaryKey,
        binaryParser,
        subscriber: (msg) => {
            if (msg.key === binaryKey || msg.key === key) {
                setPayload(msg.payload as unknown as T)
                callback && callback(msg.payload as unknown as T)
            }
        },
    })

    return payload
}
