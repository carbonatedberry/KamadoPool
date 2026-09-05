/// <reference types="svelte" />
/// <reference types="vite/client" />

// `qrcode` ships no types and has no @types package. Declare the surface we
// actually use, the donation QR in App.svelte and the proof QR on the share
// card, rather than leaving both call sites as implicit `any`.
declare module "qrcode" {
  export type QRCodeErrorCorrectionLevel = "L" | "M" | "Q" | "H";

  export interface QRCodeRenderOptions {
    width?: number;
    margin?: number;
    scale?: number;
    errorCorrectionLevel?: QRCodeErrorCorrectionLevel;
    color?: { dark?: string; light?: string };
  }

  export function toCanvas(
    canvas: HTMLCanvasElement,
    text: string,
    options?: QRCodeRenderOptions,
  ): Promise<void>;

  export function toDataURL(
    text: string,
    options?: QRCodeRenderOptions,
  ): Promise<string>;

  const QRCode: {
    toCanvas: typeof toCanvas;
    toDataURL: typeof toDataURL;
  };
  export default QRCode;
}
