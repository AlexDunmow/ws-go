import { DataType } from "./util"

export interface Message<T = DataType> {
    uri: string
    key: string | number
    payload: T
    mt: number
    transactionId?: string
}
