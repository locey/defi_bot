// lib/api/client.ts
// API client for backend communication

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export interface APIResponse<T> {
  success: boolean;
  data: T;
  count?: number;
  total?: number;
  error?: string;
}

export interface StatsData {
  total_opportunities: number;
  pending_opportunities: number;
  executed_count: number;
  success_count: number;
  failed_count: number;
  success_rate: number;
  total_profit: string;
  avg_profit_rate: number;
  last_24h_profit: string;
  last_24h_executions: number;
  last_24h_success_rate: number;
}

export interface Opportunity {
  id: number;
  token_in_id: number | null;
  token_out_id: number | null;
  arbitrage_type: string;
  amount_in: string;
  expected_profit: string;
  min_profit: string;
  profit_rate: number;
  swap_path: string;
  dex_path: string;
  dex_routers: string;
  gas_estimate: number;
  max_gas_price: string;
  status: string;
  priority: number;
  expires_at: string;
  created_at: string;
  updated_at: string;
}

export interface Execution {
  id: number;
  opportunity_id: number | null;
  vault_address: string;
  token_in_id: number | null;
  token_out_id: number | null;
  amount_in: string;
  amount_out: string;
  actual_profit: string;
  profit_rate: number;
  swap_path: string;
  dex_path: string;
  gas_used: number;
  gas_price: string;
  tx_hash: string;
  block_number: number;
  status: string;
  error_message: string;
  execution_time_ms: number;
  timestamp: string;
  created_at: string;
}

export interface DailyStats {
  date: string;
  executions: number;
  success_rate: number;
  profit: string;
  gas_spent: string;
}

export interface Token {
  id: number;
  address: string;
  symbol: string;
  name: string;
  decimals: number;
  chain_id: number;
  price_usd: number;
  is_active: boolean;
}

class APIClient {
  private baseURL: string;

  constructor(baseURL: string) {
    this.baseURL = baseURL;
  }

  private async request<T>(path: string, options?: RequestInit): Promise<T> {
    const url = `${this.baseURL}${path}`;
    
    try {
      const response = await fetch(url, {
        ...options,
        headers: {
          "Content-Type": "application/json",
          ...options?.headers,
        },
      });

      if (!response.ok) {
        throw new Error(`API Error: ${response.status} ${response.statusText}`);
      }

      const json = await response.json();
      
      if (!json.success) {
        throw new Error(json.error || "API request failed");
      }

      return json.data as T;
    } catch (error) {
      console.error(`API request failed: ${url}`, error);
      throw error;
    }
  }

  // Health check
  async healthCheck(): Promise<{ status: string; service: string }> {
    const response = await fetch(`${this.baseURL}/health`);
    return response.json();
  }

  // Stats
  async getStats(): Promise<StatsData> {
    return this.request<StatsData>("/api/v1/stats");
  }

  async getDailyStats(): Promise<DailyStats[]> {
    return this.request<DailyStats[]>("/api/v1/stats/daily");
  }

  // Opportunities
  async getOpportunities(params?: {
    status?: string;
    limit?: number;
    offset?: number;
  }): Promise<Opportunity[]> {
    const query = new URLSearchParams();
    if (params?.status) query.set("status", params.status);
    if (params?.limit) query.set("limit", params.limit.toString());
    if (params?.offset) query.set("offset", params.offset.toString());
    
    const queryStr = query.toString();
    return this.request<Opportunity[]>(
      `/api/v1/opportunities${queryStr ? `?${queryStr}` : ""}`
    );
  }

  async getOpportunityById(id: number): Promise<Opportunity> {
    return this.request<Opportunity>(`/api/v1/opportunities/${id}`);
  }

  // Executions
  async getExecutions(params?: {
    limit?: number;
    offset?: number;
  }): Promise<Execution[]> {
    const query = new URLSearchParams();
    if (params?.limit) query.set("limit", params.limit.toString());
    if (params?.offset) query.set("offset", params.offset.toString());
    
    const queryStr = query.toString();
    return this.request<Execution[]>(
      `/api/v1/executions${queryStr ? `?${queryStr}` : ""}`
    );
  }

  // Tokens
  async getTokens(): Promise<Token[]> {
    return this.request<Token[]>("/api/v1/tokens");
  }

  // Vault
  async getVaultInfo(address: string): Promise<any> {
    return this.request<any>(`/api/v1/vault/${address}`);
  }
}

export const apiClient = new APIClient(API_BASE);
export default apiClient;

