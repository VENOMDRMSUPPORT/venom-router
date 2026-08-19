#!/usr/bin/env node
import { openDb } from '../db/index.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';

const db = openDb(process.env.CATALOG_DB);
const removed = db.prepare(`
  DELETE FROM model_identity_scores
  WHERE score IS NULL AND evidence_json='["catalog-operational-facts"]'
`).run() as unknown as { changes: number | bigint };
const summary = recalculatePublishedOffers(db, new Date().toISOString());
const offerEvidence = db.prepare(`
  SELECT dimension,status,COUNT(*) n
  FROM provider_model_scores
  GROUP BY dimension,status
  ORDER BY dimension,status
`).all();
console.log(JSON.stringify({ removedLegacyApplicability: Number(removed.changes), summary, offerEvidence }, null, 2));
db.close();
