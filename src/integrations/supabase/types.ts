export type Json = string | number | boolean | null | { [key: string]: Json | undefined } | Json[];

export type Database = {
  // Allows to automatically instantiate createClient with right options
  // instead of createClient<Database, { PostgrestVersion: 'XX' }>(URL, KEY)
  __InternalSupabase: {
    PostgrestVersion: "14.5";
  };
  public: {
    Tables: {
      accounts: {
        Row: {
          auth_type: string;
          created_at: string;
          credentials_enc: string | null;
          credentials_iv: string | null;
          credentials_tag: string | null;
          email: string | null;
          id: string;
          label: string;
          last_health_check_at: string | null;
          last_synced_at: string | null;
          metadata: Json;
          plan: string | null;
          provider_id: string;
          quota_extra: Json | null;
          quota_strategy: Database["public"]["Enums"]["quota_strategy"];
          quota_total: number | null;
          quota_unit: string | null;
          quota_used: number | null;
          status: Database["public"]["Enums"]["account_status"];
          updated_at: string;
        };
        Insert: {
          auth_type?: string;
          created_at?: string;
          credentials_enc?: string | null;
          credentials_iv?: string | null;
          credentials_tag?: string | null;
          email?: string | null;
          id?: string;
          label: string;
          last_health_check_at?: string | null;
          last_synced_at?: string | null;
          metadata?: Json;
          plan?: string | null;
          provider_id: string;
          quota_extra?: Json | null;
          quota_strategy?: Database["public"]["Enums"]["quota_strategy"];
          quota_total?: number | null;
          quota_unit?: string | null;
          quota_used?: number | null;
          status?: Database["public"]["Enums"]["account_status"];
          updated_at?: string;
        };
        Update: {
          auth_type?: string;
          created_at?: string;
          credentials_enc?: string | null;
          credentials_iv?: string | null;
          credentials_tag?: string | null;
          email?: string | null;
          id?: string;
          label?: string;
          last_health_check_at?: string | null;
          last_synced_at?: string | null;
          metadata?: Json;
          plan?: string | null;
          provider_id?: string;
          quota_extra?: Json | null;
          quota_strategy?: Database["public"]["Enums"]["quota_strategy"];
          quota_total?: number | null;
          quota_unit?: string | null;
          quota_used?: number | null;
          status?: Database["public"]["Enums"]["account_status"];
          updated_at?: string;
        };
        Relationships: [
          {
            foreignKeyName: "accounts_provider_id_fkey";
            columns: ["provider_id"];
            isOneToOne: false;
            referencedRelation: "providers";
            referencedColumns: ["id"];
          },
        ];
      };
      app_settings: {
        Row: {
          key: string;
          updated_at: string;
          value: Json;
        };
        Insert: {
          key: string;
          updated_at?: string;
          value?: Json;
        };
        Update: {
          key?: string;
          updated_at?: string;
          value?: Json;
        };
        Relationships: [];
      };
      audit_log: {
        Row: {
          action: string;
          actor_user_id: string | null;
          created_at: string;
          id: string;
          metadata: Json;
          target_id: string | null;
          target_type: string | null;
        };
        Insert: {
          action: string;
          actor_user_id?: string | null;
          created_at?: string;
          id?: string;
          metadata?: Json;
          target_id?: string | null;
          target_type?: string | null;
        };
        Update: {
          action?: string;
          actor_user_id?: string | null;
          created_at?: string;
          id?: string;
          metadata?: Json;
          target_id?: string | null;
          target_type?: string | null;
        };
        Relationships: [];
      };
      model_tests: {
        Row: {
          account_id: string | null;
          error: string | null;
          id: string;
          latency_ms: number | null;
          model_id: string;
          status: string;
          tested_at: string;
        };
        Insert: {
          account_id?: string | null;
          error?: string | null;
          id?: string;
          latency_ms?: number | null;
          model_id: string;
          status: string;
          tested_at?: string;
        };
        Update: {
          account_id?: string | null;
          error?: string | null;
          id?: string;
          latency_ms?: number | null;
          model_id?: string;
          status?: string;
          tested_at?: string;
        };
        Relationships: [
          {
            foreignKeyName: "model_tests_account_id_fkey";
            columns: ["account_id"];
            isOneToOne: false;
            referencedRelation: "accounts";
            referencedColumns: ["id"];
          },
          {
            foreignKeyName: "model_tests_model_id_fkey";
            columns: ["model_id"];
            isOneToOne: false;
            referencedRelation: "models";
            referencedColumns: ["id"];
          },
        ];
      };
      account_models: {
        Row: {
          account_id: string;
          created_at: string;
          enabled: boolean;
          id: string;
          last_test_error: string | null;
          last_tested_at: string | null;
          latency_ms: number | null;
          lifecycle: Database["public"]["Enums"]["model_lifecycle"];
          model_id: string;
          test_status: string;
          updated_at: string;
        };
        Insert: {
          account_id: string;
          created_at?: string;
          enabled?: boolean;
          id?: string;
          last_test_error?: string | null;
          last_tested_at?: string | null;
          latency_ms?: number | null;
          lifecycle?: Database["public"]["Enums"]["model_lifecycle"];
          model_id: string;
          test_status?: string;
          updated_at?: string;
        };
        Update: {
          account_id?: string;
          created_at?: string;
          enabled?: boolean;
          id?: string;
          last_test_error?: string | null;
          last_tested_at?: string | null;
          latency_ms?: number | null;
          lifecycle?: Database["public"]["Enums"]["model_lifecycle"];
          model_id?: string;
          test_status?: string;
          updated_at?: string;
        };
        Relationships: [
          {
            foreignKeyName: "account_models_account_id_fkey";
            columns: ["account_id"];
            isOneToOne: false;
            referencedRelation: "accounts";
            referencedColumns: ["id"];
          },
          {
            foreignKeyName: "account_models_model_id_fkey";
            columns: ["model_id"];
            isOneToOne: false;
            referencedRelation: "models";
            referencedColumns: ["id"];
          },
        ];
      };
      models: {
        Row: {
          capabilities: Json;
          context_window: number | null;
          created_at: string;
          display_name: string;
          external_id: string;
          id: string;
          input_cost_per_mtok: number | null;
          last_tested_at: string | null;
          lifecycle: Database["public"]["Enums"]["model_lifecycle"];
          output_cost_per_mtok: number | null;
          provider_id: string;
          quality_rating: number;
          updated_at: string;
        };
        Insert: {
          capabilities?: Json;
          context_window?: number | null;
          created_at?: string;
          display_name: string;
          external_id: string;
          id?: string;
          input_cost_per_mtok?: number | null;
          last_tested_at?: string | null;
          lifecycle?: Database["public"]["Enums"]["model_lifecycle"];
          output_cost_per_mtok?: number | null;
          provider_id: string;
          quality_rating?: number;
          updated_at?: string;
        };
        Update: {
          capabilities?: Json;
          context_window?: number | null;
          created_at?: string;
          display_name?: string;
          external_id?: string;
          id?: string;
          input_cost_per_mtok?: number | null;
          last_tested_at?: string | null;
          lifecycle?: Database["public"]["Enums"]["model_lifecycle"];
          output_cost_per_mtok?: number | null;
          provider_id?: string;
          quality_rating?: number;
          updated_at?: string;
        };
        Relationships: [
          {
            foreignKeyName: "models_provider_id_fkey";
            columns: ["provider_id"];
            isOneToOne: false;
            referencedRelation: "providers";
            referencedColumns: ["id"];
          },
        ];
      };
      oauth_flows: {
        Row: {
          code_verifier: string | null;
          created_at: string;
          extra: Json | null;
          id: string;
          provider_slug: string;
          redirect_uri: string | null;
          state: string;
        };
        Insert: {
          code_verifier?: string | null;
          created_at?: string;
          extra?: Json | null;
          id?: string;
          provider_slug: string;
          redirect_uri?: string | null;
          state: string;
        };
        Update: {
          code_verifier?: string | null;
          created_at?: string;
          extra?: Json | null;
          id?: string;
          provider_slug?: string;
          redirect_uri?: string | null;
          state?: string;
        };
        Relationships: [];
      };
      providers: {
        Row: {
          adapter: string;
          auth_type: string;
          base_url: string | null;
          category: string;
          created_at: string;
          description: string | null;
          homepage: string | null;
          id: string;
          is_builtin: boolean;
          kind: Database["public"]["Enums"]["provider_kind"];
          metadata: Json;
          name: string;
          slug: string | null;
          updated_at: string;
        };
        Insert: {
          adapter: string;
          auth_type?: string;
          base_url?: string | null;
          category?: string;
          created_at?: string;
          description?: string | null;
          homepage?: string | null;
          id?: string;
          is_builtin?: boolean;
          kind: Database["public"]["Enums"]["provider_kind"];
          metadata?: Json;
          name: string;
          slug?: string | null;
          updated_at?: string;
        };
        Update: {
          adapter?: string;
          auth_type?: string;
          base_url?: string | null;
          category?: string;
          created_at?: string;
          description?: string | null;
          homepage?: string | null;
          id?: string;
          is_builtin?: boolean;
          kind?: Database["public"]["Enums"]["provider_kind"];
          metadata?: Json;
          name?: string;
          slug?: string | null;
          updated_at?: string;
        };
        Relationships: [];
      };
      quotas: {
        Row: {
          account_id: string;
          confidence: Database["public"]["Enums"]["quota_confidence"];
          resets_at: string | null;
          source: string;
          total: number | null;
          unit: string;
          updated_at: string;
          used: number;
        };
        Insert: {
          account_id: string;
          confidence?: Database["public"]["Enums"]["quota_confidence"];
          resets_at?: string | null;
          source?: string;
          total?: number | null;
          unit?: string;
          updated_at?: string;
          used?: number;
        };
        Update: {
          account_id?: string;
          confidence?: Database["public"]["Enums"]["quota_confidence"];
          resets_at?: string | null;
          source?: string;
          total?: number | null;
          unit?: string;
          updated_at?: string;
          used?: number;
        };
        Relationships: [
          {
            foreignKeyName: "quotas_account_id_fkey";
            columns: ["account_id"];
            isOneToOne: true;
            referencedRelation: "accounts";
            referencedColumns: ["id"];
          },
        ];
      };
      routing_rules: {
        Row: {
          account_id: string;
          active: boolean;
          conditions: Json;
          created_at: string;
          id: string;
          model_id: string;
          priority: number;
          role: Database["public"]["Enums"]["rule_role"];
          updated_at: string;
          venom_slug: Database["public"]["Enums"]["venom_slug"];
        };
        Insert: {
          account_id: string;
          active?: boolean;
          conditions?: Json;
          created_at?: string;
          id?: string;
          model_id: string;
          priority?: number;
          role?: Database["public"]["Enums"]["rule_role"];
          updated_at?: string;
          venom_slug: Database["public"]["Enums"]["venom_slug"];
        };
        Update: {
          account_id?: string;
          active?: boolean;
          conditions?: Json;
          created_at?: string;
          id?: string;
          model_id?: string;
          priority?: number;
          role?: Database["public"]["Enums"]["rule_role"];
          updated_at?: string;
          venom_slug?: Database["public"]["Enums"]["venom_slug"];
        };
        Relationships: [
          {
            foreignKeyName: "routing_rules_account_id_fkey";
            columns: ["account_id"];
            isOneToOne: false;
            referencedRelation: "accounts";
            referencedColumns: ["id"];
          },
          {
            foreignKeyName: "routing_rules_model_id_fkey";
            columns: ["model_id"];
            isOneToOne: false;
            referencedRelation: "models";
            referencedColumns: ["id"];
          },
          {
            foreignKeyName: "routing_rules_venom_slug_fkey";
            columns: ["venom_slug"];
            isOneToOne: false;
            referencedRelation: "venom_models";
            referencedColumns: ["slug"];
          },
        ];
      };
      routing_traces: {
        Row: {
          candidates: Json;
          created_at: string;
          fallback_chain: Json;
          id: string;
          reason: string | null;
          request_id: string;
          selected_rule_id: string | null;
          success: boolean;
          venom_slug: Database["public"]["Enums"]["venom_slug"];
        };
        Insert: {
          candidates?: Json;
          created_at?: string;
          fallback_chain?: Json;
          id?: string;
          reason?: string | null;
          request_id: string;
          selected_rule_id?: string | null;
          success?: boolean;
          venom_slug: Database["public"]["Enums"]["venom_slug"];
        };
        Update: {
          candidates?: Json;
          created_at?: string;
          fallback_chain?: Json;
          id?: string;
          reason?: string | null;
          request_id?: string;
          selected_rule_id?: string | null;
          success?: boolean;
          venom_slug?: Database["public"]["Enums"]["venom_slug"];
        };
        Relationships: [];
      };
      usage_records: {
        Row: {
          account_id: string | null;
          api_key_id: string | null;
          cost_usd: number;
          created_at: string;
          fallback_used: boolean;
          id: string;
          input_tokens: number;
          latency_ms: number | null;
          model_id: string | null;
          output_tokens: number;
          request_id: string;
          rule_id: string | null;
          success: boolean;
          venom_slug: Database["public"]["Enums"]["venom_slug"];
        };
        Insert: {
          account_id?: string | null;
          api_key_id?: string | null;
          cost_usd?: number;
          created_at?: string;
          fallback_used?: boolean;
          id?: string;
          input_tokens?: number;
          latency_ms?: number | null;
          model_id?: string | null;
          output_tokens?: number;
          request_id: string;
          rule_id?: string | null;
          success?: boolean;
          venom_slug: Database["public"]["Enums"]["venom_slug"];
        };
        Update: {
          account_id?: string | null;
          api_key_id?: string | null;
          cost_usd?: number;
          created_at?: string;
          fallback_used?: boolean;
          id?: string;
          input_tokens?: number;
          latency_ms?: number | null;
          model_id?: string | null;
          output_tokens?: number;
          request_id?: string;
          rule_id?: string | null;
          success?: boolean;
          venom_slug?: Database["public"]["Enums"]["venom_slug"];
        };
        Relationships: [
          {
            foreignKeyName: "usage_records_account_id_fkey";
            columns: ["account_id"];
            isOneToOne: false;
            referencedRelation: "accounts";
            referencedColumns: ["id"];
          },
          {
            foreignKeyName: "usage_records_model_id_fkey";
            columns: ["model_id"];
            isOneToOne: false;
            referencedRelation: "models";
            referencedColumns: ["id"];
          },
          {
            foreignKeyName: "usage_records_rule_id_fkey";
            columns: ["rule_id"];
            isOneToOne: false;
            referencedRelation: "routing_rules";
            referencedColumns: ["id"];
          },
        ];
      };
      user_roles: {
        Row: {
          created_at: string;
          id: string;
          role: Database["public"]["Enums"]["app_role"];
          user_id: string;
        };
        Insert: {
          created_at?: string;
          id?: string;
          role: Database["public"]["Enums"]["app_role"];
          user_id: string;
        };
        Update: {
          created_at?: string;
          id?: string;
          role?: Database["public"]["Enums"]["app_role"];
          user_id?: string;
        };
        Relationships: [];
      };
      venom_api_keys: {
        Row: {
          allowed_models: Database["public"]["Enums"]["venom_slug"][];
          created_at: string;
          id: string;
          key_hash: string;
          key_prefix: string;
          last_used_at: string | null;
          monthly_cap_usd: number | null;
          name: string;
          revoked_at: string | null;
          rpm_limit: number | null;
          tpd_limit: number | null;
        };
        Insert: {
          allowed_models?: Database["public"]["Enums"]["venom_slug"][];
          created_at?: string;
          id?: string;
          key_hash: string;
          key_prefix: string;
          last_used_at?: string | null;
          monthly_cap_usd?: number | null;
          name: string;
          revoked_at?: string | null;
          rpm_limit?: number | null;
          tpd_limit?: number | null;
        };
        Update: {
          allowed_models?: Database["public"]["Enums"]["venom_slug"][];
          created_at?: string;
          id?: string;
          key_hash?: string;
          key_prefix?: string;
          last_used_at?: string | null;
          monthly_cap_usd?: number | null;
          name?: string;
          revoked_at?: string | null;
          rpm_limit?: number | null;
          tpd_limit?: number | null;
        };
        Relationships: [];
      };
      venom_models: {
        Row: {
          description: string | null;
          display_name: string;
          max_fallback_attempts: number;
          slug: Database["public"]["Enums"]["venom_slug"];
          strategy_config: Json;
          timeout_ms: number;
          updated_at: string;
          weight_cost: number;
          weight_quality: number;
          weight_speed: number;
        };
        Insert: {
          description?: string | null;
          display_name: string;
          max_fallback_attempts?: number;
          slug: Database["public"]["Enums"]["venom_slug"];
          strategy_config?: Json;
          timeout_ms?: number;
          updated_at?: string;
          weight_cost?: number;
          weight_quality?: number;
          weight_speed?: number;
        };
        Update: {
          description?: string | null;
          display_name?: string;
          max_fallback_attempts?: number;
          slug?: Database["public"]["Enums"]["venom_slug"];
          strategy_config?: Json;
          timeout_ms?: number;
          updated_at?: string;
          weight_cost?: number;
          weight_quality?: number;
          weight_speed?: number;
        };
        Relationships: [];
      };
    };
    Views: {
      [_ in never]: never;
    };
    Functions: {
      has_role: {
        Args: {
          _role: Database["public"]["Enums"]["app_role"];
          _user_id: string;
        };
        Returns: boolean;
      };
      is_owner: { Args: never; Returns: boolean };
    };
    Enums: {
      account_status: "healthy" | "degraded" | "expired" | "revoked" | "unknown";
      app_role: "owner";
      model_lifecycle: "discovered" | "tested" | "approved" | "blocked";
      provider_kind: "oauth" | "free" | "paid" | "custom";
      quota_confidence: "high" | "medium" | "low";
      quota_strategy: "provider_api" | "local_estimation" | "manual";
      rule_role: "primary" | "fallback";
      venom_slug: "lite" | "pro" | "max";
    };
    CompositeTypes: {
      [_ in never]: never;
    };
  };
};

type DatabaseWithoutInternals = Omit<Database, "__InternalSupabase">;

type DefaultSchema = DatabaseWithoutInternals[Extract<keyof Database, "public">];

export type Tables<
  DefaultSchemaTableNameOrOptions extends
    | keyof (DefaultSchema["Tables"] & DefaultSchema["Views"])
    | { schema: keyof DatabaseWithoutInternals },
  TableName extends DefaultSchemaTableNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals;
  }
    ? keyof (DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"] &
        DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Views"])
    : never = never,
> = DefaultSchemaTableNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals;
}
  ? (DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"] &
      DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Views"])[TableName] extends {
      Row: infer R;
    }
    ? R
    : never
  : DefaultSchemaTableNameOrOptions extends keyof (DefaultSchema["Tables"] & DefaultSchema["Views"])
    ? (DefaultSchema["Tables"] & DefaultSchema["Views"])[DefaultSchemaTableNameOrOptions] extends {
        Row: infer R;
      }
      ? R
      : never
    : never;

export type TablesInsert<
  DefaultSchemaTableNameOrOptions extends
    | keyof DefaultSchema["Tables"]
    | { schema: keyof DatabaseWithoutInternals },
  TableName extends DefaultSchemaTableNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals;
  }
    ? keyof DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"]
    : never = never,
> = DefaultSchemaTableNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals;
}
  ? DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"][TableName] extends {
      Insert: infer I;
    }
    ? I
    : never
  : DefaultSchemaTableNameOrOptions extends keyof DefaultSchema["Tables"]
    ? DefaultSchema["Tables"][DefaultSchemaTableNameOrOptions] extends {
        Insert: infer I;
      }
      ? I
      : never
    : never;

export type TablesUpdate<
  DefaultSchemaTableNameOrOptions extends
    | keyof DefaultSchema["Tables"]
    | { schema: keyof DatabaseWithoutInternals },
  TableName extends DefaultSchemaTableNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals;
  }
    ? keyof DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"]
    : never = never,
> = DefaultSchemaTableNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals;
}
  ? DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"][TableName] extends {
      Update: infer U;
    }
    ? U
    : never
  : DefaultSchemaTableNameOrOptions extends keyof DefaultSchema["Tables"]
    ? DefaultSchema["Tables"][DefaultSchemaTableNameOrOptions] extends {
        Update: infer U;
      }
      ? U
      : never
    : never;

export type Enums<
  DefaultSchemaEnumNameOrOptions extends
    | keyof DefaultSchema["Enums"]
    | { schema: keyof DatabaseWithoutInternals },
  EnumName extends DefaultSchemaEnumNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals;
  }
    ? keyof DatabaseWithoutInternals[DefaultSchemaEnumNameOrOptions["schema"]]["Enums"]
    : never = never,
> = DefaultSchemaEnumNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals;
}
  ? DatabaseWithoutInternals[DefaultSchemaEnumNameOrOptions["schema"]]["Enums"][EnumName]
  : DefaultSchemaEnumNameOrOptions extends keyof DefaultSchema["Enums"]
    ? DefaultSchema["Enums"][DefaultSchemaEnumNameOrOptions]
    : never;

export type CompositeTypes<
  PublicCompositeTypeNameOrOptions extends
    | keyof DefaultSchema["CompositeTypes"]
    | { schema: keyof DatabaseWithoutInternals },
  CompositeTypeName extends PublicCompositeTypeNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals;
  }
    ? keyof DatabaseWithoutInternals[PublicCompositeTypeNameOrOptions["schema"]]["CompositeTypes"]
    : never = never,
> = PublicCompositeTypeNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals;
}
  ? DatabaseWithoutInternals[PublicCompositeTypeNameOrOptions["schema"]]["CompositeTypes"][CompositeTypeName]
  : PublicCompositeTypeNameOrOptions extends keyof DefaultSchema["CompositeTypes"]
    ? DefaultSchema["CompositeTypes"][PublicCompositeTypeNameOrOptions]
    : never;

export const Constants = {
  public: {
    Enums: {
      account_status: ["healthy", "degraded", "expired", "revoked", "unknown"],
      app_role: ["owner"],
      model_lifecycle: ["discovered", "tested", "approved", "blocked"],
      provider_kind: ["oauth", "free", "paid", "custom"],
      quota_confidence: ["high", "medium", "low"],
      quota_strategy: ["provider_api", "local_estimation", "manual"],
      rule_role: ["primary", "fallback"],
      venom_slug: ["lite", "pro", "max"],
    },
  },
} as const;
