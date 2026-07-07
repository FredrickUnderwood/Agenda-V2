export function errorMessage(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string }
  return e.response?.data?.error ?? e.message ?? 'Something went wrong.'
}
