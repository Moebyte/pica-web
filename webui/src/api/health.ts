import { request } from './http'

export interface GatewayHealth {
  ok: boolean
  uptime: number
  version: string
  downloads?: { queued: number; running: number }
}

export async function getHealth(): Promise<GatewayHealth> {
  return request<GatewayHealth>('/healthz')
}
