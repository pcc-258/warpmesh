declare module "@novnc/novnc" {
  export default class RFB {
    constructor(target: HTMLElement, url: string, options?: { credentials?: { password?: string } });
    scaleViewport: boolean;
    resizeSession: boolean;
    addEventListener(type: string, listener: (event: CustomEvent) => void): void;
    disconnect(): void;
    sendCredentials(credentials: { password: string }): void;
  }
}
