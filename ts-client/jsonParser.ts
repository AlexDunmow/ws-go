const DateParse = () => {
    const reISO =
        /^([0-9]+)-(0[1-9]|1[012])-(0[1-9]|[12][0-9]|3[01])[Tt]([01][0-9]|2[0-3]):([0-5][0-9]):([0-5][0-9]|60)(\.[0-9]+)?(([Zz])|([+|-]([01][0-9]|2[0-3]):[0-5][0-9]))$/

    return function (key: string, value: unknown) {
        if (typeof value === "string") {
            const a = reISO.exec(value)
            if (a) return new Date(value)
        }
        return value
    }
}

const dateParser = DateParse()

export default dateParser
