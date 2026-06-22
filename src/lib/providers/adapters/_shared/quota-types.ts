/* Shared quota / model-info types. Pure types — safe everywhere. */

export interface QuotaPeriod {
  remainingFraction: number;
  resetTime: string;
  isExhausted: boolean;
}

export interface ModelQuotaInfo {
  remainingFraction: number;
  resetTime: string;
  isExhausted: boolean;
  weeklyQuota?: QuotaPeriod;
  fiveHourQuota?: QuotaPeriod;
}

export interface QuotaGroup {
  name: string;
  modelIds: string[];
  weeklyQuota?: QuotaPeriod;
  fiveHourQuota?: QuotaPeriod;
}

export interface RichDiscoveredModel {
  external_id: string;
  display_name: string;
  capabilities: string[];
  context_window?: number;
  max_output_tokens?: number;
  quota?: ModelQuotaInfo;
}
