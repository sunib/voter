// The browser entry point of `qrcode`, typed by hand.
//
// @types/qrcode is not used on purpose: it references Node's types, and pulling
// @types/node into this project undoes tsconfig.app.json's `types:
// ["vite/client"]` -- which exists so the browser bundle's type environment
// cannot see Node. The first symptom was window.setInterval resolving to Node's
// overload and no longer returning a number.
declare module 'qrcode/lib/browser' {
  export interface QRCodeToStringOptions {
    type?: 'svg' | 'utf8' | 'terminal'
    margin?: number
    width?: number
    errorCorrectionLevel?: 'L' | 'M' | 'Q' | 'H'
    color?: { dark?: string; light?: string }
  }
  export function toString(
    text: string,
    options?: QRCodeToStringOptions,
  ): Promise<string>
}
