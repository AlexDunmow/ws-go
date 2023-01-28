import { useWS } from "./useWS"
import { useEffect, useRef, useState } from "react"
import { Message } from "./message"
import { DataType, makeid } from "./util"
import { Socket } from "./context"

interface WSError {
    transaction_id: string
    key: string
    error: string
}

export interface CommandProps {
    URI: string
    host?: string
    ready?: boolean
}

export type SendFunc = <Y = DataType, X = DataType>(key: string, payload?: X) => Promise<Y>
type Callback<T> = (data: Message<T> | WSError) => void

export const useCommands = ({ host, URI, ready }: CommandProps) => {
    const [toSend, setToSend] = useState<string[]>([])
    const backlog = useRef<string[]>([])
    const sending = useRef<boolean>(false)
    const callbacks = useRef<{ [key: string]: Callback<DataType> }>({})

    const { state, key } = useWS({
        URI,
        host,
        ready,
        subscriber: (msg) => {
            if (!msg.transactionId) return
            const { [msg.transactionId]: cb, ...withoutCb } = callbacks.current
            if (cb) {
                callbacks.current = { ...withoutCb }
                cb(msg)
            }
        },
        isCommander: true,
    })

    useEffect(() => {
        if (toSend.length === 0 || sending.current) return
        if (state === WebSocket.OPEN) {
            sending.current = true
            const notSent = [...toSend, ...backlog.current]

            const sock = Socket(key, true)

            try {
                for (let i = 0; i < toSend.length; i++) {
                    if (sock) sock.send(toSend[i])
                    notSent.shift()
                }
            } catch (err) {
                console.error(err)
            }
            backlog.current = []
            setToSend(notSent)
            sending.current = false
        }
    }, [key, state, toSend, sending])

    const send = useRef<SendFunc>(function send<Y = unknown, X = unknown>(key: string, payload?: X): Promise<Y> {
        return new Promise(function (resolve, reject) {
            const transactionId = makeid()
            const s = JSON.stringify({
                key,
                payload,
                transactionId,
            })
            callbacks.current[transactionId] = (data: Message | WSError) => {
                if (data.key === "ERROR") {
                    console.error("ws err", data, s)
                    reject((data as WSError).error)
                    return
                }
                const result = (data as unknown as Message<Y>).payload
                resolve(result)
            }
            if (sending.current) {
                backlog.current.push(s)
                return
            }
            setToSend((b) => {
                if (b.indexOf(s) > -1) return b
                return [...b, s]
            })
        })
    })

    return { send: send.current, state }
}
