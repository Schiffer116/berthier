# Built from scratch rather than on firstmate

firstmate (`~/firstmate`) already orchestrates a crew of agents over Herdr, but it is bloated and burns tokens: a large always-loaded instruction set and LLM-driven supervision for things a script could decide. Berthier is built from scratch to stay small, keep the LLM out of everything deterministic (see ADR 0001), and spend tokens only on judgment calls.
