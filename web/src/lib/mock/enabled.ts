// Mock switch. A public variable injected at build time (only the NEXT_PUBLIC_ prefix is readable in the browser).
// Setting NEXT_PUBLIC_MOCK=1 on Vercel makes the whole site use mocks, with no backend needed.
export const MOCK = process.env.NEXT_PUBLIC_MOCK === "1";
