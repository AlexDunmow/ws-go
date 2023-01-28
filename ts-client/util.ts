export function makeid(length: number = 12): string {
    let result = ""
    const characters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
    for (let i = 0; i < length; i++) {
        result += characters.charAt(Math.floor(Math.random() * characters.length))
    }
    return result
}

export function protocol() {
    if (typeof window !== "undefined") {
        return window.location.protocol.match(/^https/) ? "wss" : "ws"
    }
}

export type DataType = { [key: string]: unknown } | string | number | unknown
