import json
import skl2onnx
from skl2onnx.common.data_types import FloatTensorType
from sklearn.ensemble import RandomForestClassifier
from sklearn.metrics import f1_score
from sklearn.model_selection import train_test_split
from sklearn.preprocessing import StandardScaler
from sklearn.compose import ColumnTransformer
import pandas as pd

# Load JSON (or CSV)
with open('ai_dataset.json', 'r') as f:
    data = json.load(f)

# Extract the count from the JSON
orderCount = data['count']
createdDate = data['createdAt']
print("No of records: ", orderCount)
print("Created date: ", createdDate)

# Create DataFrame from the order array
df = pd.DataFrame(data['orders'])

# Preprocess (fact-check: Standard for imbalanced fraud data)
X = df[['amount', 'transactionVelocity', 'amountDeviation']]  # Features (no IP)
y = df['isFraud'].astype(int)

# Pipeline: Scale numerics only
preprocessor = ColumnTransformer(
    transformers=[
        ('num', StandardScaler(), ['amount', 'transactionVelocity', 'amountDeviation'])
    ])
X_processed = preprocessor.fit_transform(X)

# No sparse issue now; directly use
# Split
X_train, X_test, y_train, y_test = train_test_split(X_processed, y, test_size=0.2, random_state=42)

# Simple RF (better for fraud; fact-check: Handles imbalance, interpretable)
model = RandomForestClassifier(n_estimators=100, class_weight='balanced', random_state=42)
model.fit(X_train, y_train)

# Evaluate (F1 for imbalanced fraud; aim >0.7 on sim data)
y_pred = model.predict(X_test)
f1 = f1_score(y_test, y_pred)
print(f"F1 Score: {f1}")

# Export to ONNX (fact-check: skl2onnx standard for scikit models)
initial_type = [('input', FloatTensorType([None, X_train.shape[1]]))]
onnx_model = skl2onnx.convert_sklearn(model, initial_types=initial_type, target_opset=12)
with open("fraud_model.onnx", "wb") as f:
    f.write(onnx_model.SerializeToString())
print("Model exported to ONNX successfully")
