interface UmbrellaInjectedParameters {
    HttpPrefix: string;
}

interface Window {
    __injected__: UmbrellaInjectedParameters;
}