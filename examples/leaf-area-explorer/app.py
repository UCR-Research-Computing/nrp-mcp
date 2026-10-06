"""Leaf area explorer: a tiny demo web tool for nrp-mcp (synthetic data only)."""

import numpy as np
import pandas as pd
import streamlit as st

st.set_page_config(page_title="Leaf area explorer", layout="centered")
st.title("Leaf area explorer")
st.caption("Demo app for nrp-mcp on NRP Nautilus. Synthetic data only.")

n = st.slider("Leaves per site", 10, 500, 100)
rng = np.random.default_rng(42)
df = pd.DataFrame(
    {
        "site": np.repeat(["A", "B", "C"], n),
        "leaf_area_cm2": np.concatenate(
            [rng.normal(m, 2.0, n) for m in (12.0, 9.5, 14.0)]
        ),
    }
)
st.bar_chart(df.groupby("site")["leaf_area_cm2"].mean())
st.dataframe(df.groupby("site")["leaf_area_cm2"].describe().round(2))
